package featureflag

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/goravel/framework/contracts/config"
	"github.com/goravel/framework/contracts/database/orm"
	"github.com/goravel/framework/contracts/log"
	"github.com/goravel/framework/errors"
	redisfacades "github.com/goravel/redis/facades"

	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/contracts"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/drivers"
)

// DriverCreator builds the driver configured under featureflag.stores.<name>.
type DriverCreator struct {
	config config.Config
	log    log.Log
	orm    orm.Orm
}

// NewDriverCreator returns a creator over the app's configuration. orm is nil
// in an app without the ORM installed; only the "database" driver needs it.
func NewDriverCreator(config config.Config, log log.Log, orm orm.Orm) *DriverCreator {
	return &DriverCreator{
		config: config,
		log:    log,
		orm:    orm,
	}
}

// Create builds the named store.
func (r *DriverCreator) Create(store string) (contracts.Driver, error) {
	return r.create(store, nil)
}

// create is Create with the chain of store names already visited. The
// "cached" driver names two other stores, so this recurses; carrying the chain
// makes a store defined in terms of itself fail here -- at boot, where the
// container resolves the manager -- instead of recursing forever on the first
// flag read.
func (r *DriverCreator) create(name string, path []string) (contracts.Driver, error) {
	if slices.Contains(path, name) {
		return nil, ErrStoreCycle.Args(name, strings.Join(append(slices.Clone(path), name), " -> "))
	}
	path = slices.Concat(path, []string{name})

	key := "featureflag.stores." + name
	switch driver := r.config.GetString(key + ".driver"); driver {
	case "memory":
		return drivers.NewMemory(), nil
	case "redis":
		return r.redis(key)
	case "database":
		return r.database(key)
	case "cached":
		return r.cached(name, key, path)
	case "":
		return nil, ErrStoreNotConfigured.Args(name)
	default:
		return nil, ErrDriverNotSupported.Args(driver, name)
	}
}

func (r *DriverCreator) redis(key string) (contracts.Driver, error) {
	conn := r.config.GetString(key+".connection", "default")
	prefix := r.config.GetString(key+".prefix", "feature_flag")
	client, err := redisfacades.Instance(conn)
	if err != nil {
		return nil, fmt.Errorf("featureflag: redis instance %q: %w", conn, err)
	}
	var opts []drivers.RedisOption
	// Expiry is per hash field (HEXPIRE), so a configured TTL requires Redis 7.4+.
	if ttl := r.config.GetInt(key+".ttl", 0); ttl > 0 {
		opts = append(opts, drivers.WithTTL(time.Duration(ttl)*time.Second))
	}
	return drivers.NewRedis(client, prefix, opts...), nil
}

// database runs the store over the ORM's default connection, the one the rest
// of the app already uses.
func (r *DriverCreator) database(key string) (contracts.Driver, error) {
	if r.orm == nil {
		return nil, errors.OrmFacadeNotSet.SetModule(Name)
	}
	db, err := r.orm.DB()
	if err != nil {
		return nil, fmt.Errorf("featureflag: database connection: %w", err)
	}
	return drivers.NewDatabase(db, r.config.GetString(key+".table")), nil
}

func (r *DriverCreator) cached(name, key string, path []string) (contracts.Driver, error) {
	cacheName := r.config.GetString(key + ".cache")
	originName := r.config.GetString(key + ".store")
	if cacheName == "" || originName == "" {
		return nil, ErrCachedStoreIncomplete.Args(name)
	}
	cache, err := r.create(cacheName, path)
	if err != nil {
		return nil, err
	}
	origin, err := r.create(originName, path)
	if err != nil {
		return nil, err
	}
	// drivers.Cached never logs on its own; this is how a tolerated cache failure
	// reaches the app's log.
	return drivers.NewCached(cache, origin,
		drivers.WithCacheErrorHandler(func(err error) {
			r.log.Warningf("featureflag: cache degraded: %v", err)
		})), nil
}

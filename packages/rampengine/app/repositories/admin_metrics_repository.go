package repositories

import (
	"fmt"
	"time"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/support/period"
)

// AdminMetricsRepository is the persistence boundary for the admin metrics
// endpoints — the overview's counters and the time series behind the
// dashboard chart. Every method is a SQL aggregate — no row hydration — and every
// query goes through the model so the ORM's soft-delete global scope applies.
// The repository never parses dates: callers pass an already-validated
// period.Period.
type AdminMetricsRepository interface {
	// CountUsers is the all-time user count. Period does not apply.
	CountUsers() (int64, error)
	// CountUsersCreated counts users whose created_at falls in p.
	CountUsersCreated(p period.Period) (int64, error)
	// CountTransactionsByStatus groups transactions in p by their raw status.
	// The map is sparse; zero-filling canonical statuses is the resource's job.
	CountTransactionsByStatus(p period.Period) (map[string]int64, error)
	// CountTransactionsByType groups transactions in p by canonical type,
	// folding the legacy payin/payout names into onramp/offramp.
	CountTransactionsByType(p period.Period) (map[string]int64, error)
	// SumCompletedVolumeByToken sums amount_fiat for completed transactions in
	// p, grouped by UPPER(target_token). Volume is returned as the raw
	// decimal string from the database — never as float64.
	SumCompletedVolumeByToken(p period.Period) ([]TokenVolume, error)
	// SumCompletedUSDVolumeByToken is SumCompletedVolumeByToken for the USD
	// rows: the same grouping, the dollar amounts. The two are separate calls
	// because a figure must never add reais and dollars together.
	SumCompletedUSDVolumeByToken(p period.Period) ([]TokenVolume, error)
	// CountKycByStatus is the all-time KYC status breakdown. Period does not
	// apply.
	CountKycByStatus() (map[string]int64, error)
	// CountCustomersByType is the all-time count of live customers — who holds
	// the money — by type. Period does not apply.
	CountCustomersByType() (map[string]int64, error)
	// CountCustomersCreated counts live customers whose created_at falls in p.
	CountCustomersCreated(p period.Period) (int64, error)
	// TransactionDateExtent reports the created_at of the oldest and newest
	// transaction in the table, both nil when there is none. It is what an
	// omitted from/to resolves to: a time series has to be zero-filled between
	// two real dates, so "all-time" needs the dates the data actually spans.
	TransactionDateExtent() (earliest, latest *time.Time, err error)
	// TransactionBucketAggregates groups the transactions in p into buckets of
	// size g, reporting per group the transaction count across every status
	// and the BRL volume of the completed ones only — the same slice of rows
	// as SumCompletedVolumeByToken, so a series adds up to the overview.
	//
	// Types arrive canonicalised, which means a bucket holding both `payin`
	// and `onramp` rows yields two entries carrying the same type. Folding
	// them is the caller's job: summing decimal volume belongs in the service,
	// not in a query. The result is sparse — buckets with no transaction are
	// absent, and zero-filling them is likewise the caller's job.
	TransactionBucketAggregates(p period.Period, g period.Granularity) ([]BucketAggregate, error)
}

// TokenVolume is one GROUP BY UPPER(target_token) row from the completed
// transaction volume aggregate.
type TokenVolume struct {
	Token  string // already UPPER'd
	Volume string // numeric as string from the DB, e.g. "14000.00"
}

// BucketAggregate is one (time bucket, transaction type) group of the time
// series aggregate.
type BucketAggregate struct {
	Bucket time.Time // start of the bucket, midnight UTC
	Type   string    // canonical type: onramp or offramp
	Count  int64     // transactions created in the bucket, every status
	Volume string    // completed-only BRL numeric as string, e.g. "14000.00"
	// VolumeUSD is the completed-only USD volume of the same group, kept apart
	// from Volume so no figure mixes the two currencies.
	VolumeUSD string
}

type adminMetricsRepository struct{}

// NewAdminMetricsRepository returns an AdminMetricsRepository backed by the
// default ORM connection.
func NewAdminMetricsRepository() AdminMetricsRepository {
	return &adminMetricsRepository{}
}

// applyCreatedAt narrows q to p's half-open created_at bounds. Both bounds
// nil leaves q untouched (all-time).
func applyCreatedAt(q orm.Query, p period.Period) orm.Query {
	if p.FromDate != nil {
		q = q.Where("created_at >= ?", *p.FromDate)
	}
	if p.ToExclusive != nil {
		q = q.Where("created_at < ?", *p.ToExclusive)
	}
	return q
}

func (r *adminMetricsRepository) CountUsers() (int64, error) {
	count, err := facades.Orm().Query().Model(&models.User{}).Count()
	if err != nil {
		return 0, fmt.Errorf("admin metrics repo: count users: %w", err)
	}
	return count, nil
}

func (r *adminMetricsRepository) CountUsersCreated(p period.Period) (int64, error) {
	q := facades.Orm().Query().Model(&models.User{})
	q = applyCreatedAt(q, p)
	count, err := q.Count()
	if err != nil {
		return 0, fmt.Errorf("admin metrics repo: count users created: %w", err)
	}
	return count, nil
}

// statusCount is the scan target for GROUP BY status/kyc_status aggregates.
type statusCount struct {
	Status string
	Count  int64
}

func (r *adminMetricsRepository) CountTransactionsByStatus(p period.Period) (map[string]int64, error) {
	q := facades.Orm().Query().Model(&models.Transaction{})
	q = applyCreatedAt(q, p)

	var rows []statusCount
	if err := q.Select("status", "COUNT(*) AS count").Group("status").Get(&rows); err != nil {
		return nil, fmt.Errorf("admin metrics repo: count transactions by status: %w", err)
	}

	result := make(map[string]int64, len(rows))
	for _, row := range rows {
		result[row.Status] = row.Count
	}
	return result, nil
}

// typeCount is the scan target for the by_type aggregate before legacy names
// are folded into their canonical counterparts.
type typeCount struct {
	Type  string
	Count int64
}

// kycStatusCount is the scan target for GROUP BY kyc_status aggregates.
type kycStatusCount struct {
	KycStatus string
	Count     int64
}

// canonicalTransactionType maps legacy type names onto the canonical
// onramp/offramp vocabulary; canonical names map to themselves.
func canonicalTransactionType(t string) string {
	switch t {
	case "payin":
		return models.TransactionTypeOnramp
	case "payout":
		return models.TransactionTypeOfframp
	default:
		return t
	}
}

func (r *adminMetricsRepository) CountTransactionsByType(p period.Period) (map[string]int64, error) {
	q := facades.Orm().Query().Model(&models.Transaction{})
	q = applyCreatedAt(q, p)

	var rows []typeCount
	if err := q.Select("type", "COUNT(*) AS count").Group("type").Get(&rows); err != nil {
		return nil, fmt.Errorf("admin metrics repo: count transactions by type: %w", err)
	}

	result := make(map[string]int64, len(rows))
	for _, row := range rows {
		result[canonicalTransactionType(row.Type)] += row.Count
	}
	return result, nil
}

func (r *adminMetricsRepository) SumCompletedVolumeByToken(p period.Period) ([]TokenVolume, error) {
	// amount_fiat holds the fiat leg in fiat_currency, so a USD row's amount is
	// dollars: the BRL volume sums the BRL rows only.
	rows, err := sumCompletedVolumeByToken(p, models.FiatCurrencyBRL)
	if err != nil {
		return nil, fmt.Errorf("admin metrics repo: sum completed volume by token: %w", err)
	}
	return rows, nil
}

func (r *adminMetricsRepository) SumCompletedUSDVolumeByToken(p period.Period) ([]TokenVolume, error) {
	rows, err := sumCompletedVolumeByToken(p, models.FiatCurrencyUSD)
	if err != nil {
		return nil, fmt.Errorf("admin metrics repo: sum completed usd volume by token: %w", err)
	}
	return rows, nil
}

// sumCompletedVolumeByToken sums the completed rows settled in one fiat
// currency, grouped by token.
func sumCompletedVolumeByToken(p period.Period, currency string) ([]TokenVolume, error) {
	q := facades.Orm().Query().Model(&models.Transaction{}).
		Where("status", models.TxStatusCompleted).
		Where("fiat_currency", currency)
	q = applyCreatedAt(q, p)

	var rows []TokenVolume
	if err := q.Select("UPPER(target_token) AS token", "SUM(amount_fiat)::text AS volume").
		Group("UPPER(target_token)").
		Get(&rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *adminMetricsRepository) CountKycByStatus() (map[string]int64, error) {
	var rows []kycStatusCount
	err := facades.Orm().Query().Model(&models.CustomerProviderKyc{}).
		Select("kyc_status", "COUNT(*) AS count").
		Group("kyc_status").
		Get(&rows)
	if err != nil {
		return nil, fmt.Errorf("admin metrics repo: count kyc by status: %w", err)
	}

	result := make(map[string]int64, len(rows))
	for _, row := range rows {
		result[row.KycStatus] = row.Count
	}
	return result, nil
}

// customerTypeCount is the scan target for the GROUP BY type aggregate.
type customerTypeCount struct {
	Type  string
	Count int64
}

func (r *adminMetricsRepository) CountCustomersByType() (map[string]int64, error) {
	var rows []customerTypeCount
	if err := facades.Orm().Query().Model(&models.Customer{}).
		Select("type", "COUNT(*) AS count").Group("type").Get(&rows); err != nil {
		return nil, fmt.Errorf("admin metrics repo: count customers by type: %w", err)
	}
	result := make(map[string]int64, len(rows))
	for _, row := range rows {
		result[row.Type] = row.Count
	}
	return result, nil
}

func (r *adminMetricsRepository) CountCustomersCreated(p period.Period) (int64, error) {
	count, err := applyCreatedAt(facades.Orm().Query().Model(&models.Customer{}), p).Count()
	if err != nil {
		return 0, fmt.Errorf("admin metrics repo: count customers created: %w", err)
	}
	return count, nil
}

// dateExtent is the scan target for the MIN/MAX created_at aggregate. Both
// columns are NULL on an empty table, so both fields are pointers.
type dateExtent struct {
	Earliest *time.Time
	Latest   *time.Time
}

func (r *adminMetricsRepository) TransactionDateExtent() (*time.Time, *time.Time, error) {
	var rows []dateExtent
	if err := facades.Orm().Query().Model(&models.Transaction{}).
		SelectRaw("MIN(created_at) AS earliest, MAX(created_at) AS latest").
		Get(&rows); err != nil {
		return nil, nil, fmt.Errorf("admin metrics repo: transaction date extent: %w", err)
	}
	// An aggregate with no GROUP BY always returns exactly one row, but an
	// empty result would panic on rows[0] rather than report anything useful.
	if len(rows) == 0 {
		return nil, nil, nil
	}
	return rows[0].Earliest, rows[0].Latest, nil
}

// bucketExpr is the SQL that truncates created_at to the start of its bucket.
//
// The granularity is switched onto a fixed literal rather than interpolated,
// so nothing a caller supplies can reach the statement — and the same string
// can then be reused verbatim in GROUP BY, which takes no bind parameters.
//
// created_at is `timestamp without time zone` holding UTC (config/app.go pins
// the app to UTC), so date_trunc needs no zone conversion and the bucket
// boundaries line up with the UTC window period.ParseYYYYMMDD builds. Bucketing
// in any other zone would put a transaction on a different day than the
// from/to filter that selected it.
func bucketExpr(g period.Granularity) string {
	switch g {
	case period.GranularityWeek:
		return "date_trunc('week', created_at)"
	case period.GranularityMonth:
		return "date_trunc('month', created_at)"
	default:
		return "date_trunc('day', created_at)"
	}
}

func (r *adminMetricsRepository) TransactionBucketAggregates(p period.Period, g period.Granularity) ([]BucketAggregate, error) {
	bucket := bucketExpr(g)

	q := facades.Orm().Query().Model(&models.Transaction{})
	q = applyCreatedAt(q, p)

	// One scan answers both numbers. Running count and volume as two queries
	// would let them disagree under concurrent writes, and the whole point of
	// the series is that its parts add up. The count spans every currency; each
	// volume sums one currency only (amount_fiat holds dollars on a USD row), so
	// reais and dollars never land in the same figure.
	var rows []BucketAggregate
	if err := q.
		SelectRaw(
			bucket+" AS bucket, type, COUNT(*) AS count,"+
				" COALESCE(SUM(amount_fiat) FILTER (WHERE status = ? AND fiat_currency = ?), 0.00)::text AS volume,"+
				" COALESCE(SUM(amount_fiat) FILTER (WHERE status = ? AND fiat_currency = ?), 0.00)::text AS volume_usd",
			models.TxStatusCompleted, models.FiatCurrencyBRL,
			models.TxStatusCompleted, models.FiatCurrencyUSD,
		).
		Group(bucket + ", type").
		OrderByRaw(bucket + ", type").
		Get(&rows); err != nil {
		return nil, fmt.Errorf("admin metrics repo: transaction bucket aggregates: %w", err)
	}

	for i := range rows {
		rows[i].Bucket = rows[i].Bucket.UTC()
		rows[i].Type = canonicalTransactionType(rows[i].Type)
	}
	return rows, nil
}

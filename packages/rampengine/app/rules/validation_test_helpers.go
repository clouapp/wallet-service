package rules

type mapData map[string]any

func (m mapData) Get(key string) (any, bool) {
	v, ok := m[key]
	return v, ok
}

func (m mapData) Set(key string, val any) error {
	m[key] = val
	return nil
}

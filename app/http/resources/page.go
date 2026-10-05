package resources

// Page is the list envelope this API already answers: data, total, limit, offset.
type Page[T any] struct {
	Data   T     `json:"data"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

// NewPage builds that envelope. data is left as given, including nil.
func NewPage[T any](data T, total int64, limit, offset int) Page[T] {
	return Page[T]{Data: data, Total: total, Limit: limit, Offset: offset}
}

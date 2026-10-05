package resources

// Page is the list envelope this API already answers: data, total, limit, offset.
type Page[T any] struct {
	Data   T     `json:"data"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

// PageDeps is the rows and the window of one list envelope.
// Data is left as given, including a nil slice.
type PageDeps[T any] struct {
	Data   T
	Total  int64
	Limit  int
	Offset int
}

// NewPage builds that envelope. data is left as given, including nil.
func NewPage[T any](deps PageDeps[T]) Page[T] {
	return Page[T]{Data: deps.Data, Total: deps.Total, Limit: deps.Limit, Offset: deps.Offset}
}

package analysis

import "context"

type AnalysisData interface {
	IsEmpty() bool
	String() string
	LoadData(ctx context.Context) error
}

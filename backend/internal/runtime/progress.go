package runtime

import "context"

type progressKey struct{}

// WithProgress observes actual runtime operations within this request.
func WithProgress(ctx context.Context, report func(string)) context.Context {
	return context.WithValue(ctx, progressKey{}, report)
}
func progress(ctx context.Context, message string) {
	if report, ok := ctx.Value(progressKey{}).(func(string)); ok {
		report(message)
	}
}

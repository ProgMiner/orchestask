package util

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
)

type logScope struct {
	prev  *logScope
	scope string
}

type logScopeKey struct{}

func WithLoggingScope(ctx context.Context, scope string) context.Context {
	return context.WithValue(ctx, logScopeKey{}, &logScope{getLogScope(ctx), scope})
}

func Log(ctx context.Context, format string, args ...any) {
	scopes := listLogScopes(ctx)

	newArgs := []any{}
	var newFormat strings.Builder
	for _, scope := range scopes {
		newArgs = append(newArgs, scope)
		newFormat.WriteString("[%s] ")
	}

	newFormat.WriteString(format)
	newFormat.WriteRune('\n')

	newArgs = append(newArgs, args...)
	fmt.Fprintf(os.Stderr, newFormat.String(), newArgs...)
}

func getLogScope(ctx context.Context) *logScope {
	val := ctx.Value(logScopeKey{})
	if val == nil {
		return nil
	}

	return val.(*logScope)
}

func listLogScopes(ctx context.Context) []string {
	scopes := []string{}

	node := getLogScope(ctx)
	for node != nil {
		scopes = append(scopes, node.scope)
		node = node.prev
	}

	slices.Reverse(scopes)
	return scopes
}

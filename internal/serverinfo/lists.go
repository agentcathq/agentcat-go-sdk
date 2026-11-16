package serverinfo

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Page is one page of a list as the server's handler returned it. Each item
// is a value that marshals to the JSON object the MCP library puts on the
// wire for that item.
type Page struct {
	Items      []any
	NextCursor string
}

// ListFunc calls the server's handler for one page of a list. An empty
// cursor asks for the first page.
type ListFunc func(ctx context.Context, cursor string) (Page, error)

const (
	// maxPages bounds the pages of one list.
	maxPages = 100

	// listTimeout bounds one list, all of its pages together.
	listTimeout = 5 * time.Second
)

var (
	errCursorRepeated = errors.New("list cursor repeated")
	errTooManyPages   = fmt.Errorf("list has more than %d pages", maxPages)
)

// collectList follows the cursor of a list until no cursor comes back. The
// list fails when a cursor repeats, when it has more than maxPages pages, when
// it takes longer than listTimeout, or when the handler fails or panics. A
// handler that ignores its context is left running; its result is discarded.
// A handler's error or panic is reported by its type only, since its text
// can quote the customer's data.
func collectList(ctx context.Context, list ListFunc) ([]any, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()

	type outcome struct {
		items []any
		err   error
	}
	done := make(chan outcome, 1)
	goGuarded(func(recovered any) {
		done <- outcome{err: fmt.Errorf("list handler panicked (%T)", recovered)}
	}, func() {
		items, err := pageThrough(ctx, list)
		done <- outcome{items: items, err: err}
	})

	select {
	case out := <-done:
		return out.items, out.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func pageThrough(ctx context.Context, list ListFunc) ([]any, error) {
	items := []any{}
	seen := map[string]struct{}{}
	cursor := ""
	for page := 1; ; page++ {
		if page > maxPages {
			return nil, errTooManyPages
		}
		p, err := list(ctx, cursor)
		if err != nil {
			return nil, fmt.Errorf("list handler failed (%T)", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		items = append(items, p.Items...)
		if p.NextCursor == "" {
			return items, nil
		}
		if _, repeated := seen[p.NextCursor]; repeated {
			return nil, errCursorRepeated
		}
		seen[p.NextCursor] = struct{}{}
		cursor = p.NextCursor
	}
}

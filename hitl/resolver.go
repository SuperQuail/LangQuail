package hitl

import "context"

type Resolver interface {
	Resolve(context.Context, string, Response) error
}

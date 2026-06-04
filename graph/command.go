package graph

type Interrupt struct {
	ID           string
	Kind         string
	Reason       string
	Payload      any
	RunID        string
	NodeID       string
	CheckpointID string
	ResumeNode   string
}

type Command[S any] struct {
	Update    *S
	Goto      string
	Interrupt *Interrupt
	End       bool
}

func Update[S any](state S) Command[S] {
	return Command[S]{Update: &state}
}

func Goto[S any](target string) Command[S] {
	return Command[S]{Goto: target}
}

func UpdateAndGoto[S any](state S, target string) Command[S] {
	return Command[S]{Update: &state, Goto: target}
}

func End[S any]() Command[S] {
	return Command[S]{End: true}
}

func Noop[S any]() Command[S] {
	return Command[S]{}
}

func InterruptRun[S any](reason string, payload any) Command[S] {
	return Command[S]{
		Interrupt: &Interrupt{
			Reason:  reason,
			Payload: payload,
		},
	}
}

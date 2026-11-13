package oam

// RunState returns the value this run keeps under key, and makes it with newState the first
// time a rule asks. A model rule keeps what the whole build has spent there, so the state
// belongs to one Transform and a rule shared by two builds shares nothing. Without an
// allocator there is no run, and every call gets a new value.
func (l LoweringContext) RunState(key any, newState func() any) any {
	if l.Namer == nil {
		return newState()
	}
	if v, ok := l.Namer.runState[key]; ok {
		return v
	}
	if l.Namer.runState == nil {
		l.Namer.runState = map[any]any{}
	}
	v := newState()
	l.Namer.runState[key] = v
	return v
}

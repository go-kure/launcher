package model

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	"github.com/google/cel-go/interpreter"

	"github.com/go-kure/launcher/pkg/errors"
)

// ErrCostLimit is wrapped by the failure of an evaluation that spent more than the cost
// limit of one expression.
var ErrCostLimit = errors.New("model: expression cost limit exceeded")

// ErrNonFinite is wrapped by the failure of an evaluation whose result holds a NaN or an
// infinite double. A document cannot encode such a value.
var ErrNonFinite = errors.New("model: result is not a finite number")

// Self is the `self` variable of an expression: the names of the component being
// built and of its application.
type Self struct {
	Name        string
	Application string
}

// Scope is what one evaluation reads: the inputs, `self`, the values computed so far,
// and the loop variable of a forEach part. An input that is absent from Inputs is null.
type Scope struct {
	Inputs map[string]any
	Self   Self
	Values map[string]any
	Loop   any
}

// segment is one piece of a template: literal text, or a compiled hole when prog is set.
type segment struct {
	text string
	prog cel.Program
}

// site is one compiled expression site. A single hole (`${ }` and no other text) keeps the
// type of its expression. Any other template makes a string.
type site struct {
	segs   []segment
	single bool
	loop   string    // the loop variable the site reads, or empty
	out    *cel.Type // the type of the result
}

// Compiled is a model whose expression sites are compiled and typed. It is safe for
// concurrent use.
type Compiled struct {
	inputs []string // the declared input names, sorted
	sites  map[string]*site
}

// Cache holds compiled sites keyed by the model identity (Model.Key), the site path and
// the template text. It is safe for concurrent use. A model identity names one model
// definition: a changed definition under the same identity must not reuse a cache.
type Cache struct {
	mu    sync.Mutex
	sites map[string]*site
}

// NewCache returns an empty cache.
func NewCache() *Cache { return &Cache{sites: map[string]*site{}} }

// Len returns the number of cached sites. A site is one expression path of one model.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.sites)
}

func (c *Cache) get(k string) *site {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sites[k]
}

// put stores s unless a concurrent compile stored one first, and returns the stored site.
func (c *Cache) put(k string, s *site) *site {
	c.mu.Lock()
	defer c.mu.Unlock()
	if old := c.sites[k]; old != nil {
		return old
	}
	c.sites[k] = s
	return s
}

// Compile compiles m with a cache of its own.
func Compile(m *Model) (*Compiled, error) { return NewCache().Compile(m) }

// Compile compiles every expression site of m, reusing the sites already in the cache.
// It reports the first failure, which wraps ErrInvalid and names the site. The order is
// the values, then for each part: when, forEach.in, name, the properties (keys sorted at
// every level), the trait properties and the annotations.
func (c *Cache) Compile(m *Model) (*Compiled, error) {
	w := &walker{cache: c, key: m.Key(), declared: scopeTypes(m), notYet: map[string]bool{}, sites: map[string]*site{}}
	for _, v := range m.Spec.Values {
		w.notYet[v.Name] = true
	}
	for i, v := range m.Spec.Values {
		s, err := w.site("spec.values["+strconv.Itoa(i)+"].expr", v.Expr, siteSpec{strict: true})
		if err != nil {
			return nil, err
		}
		delete(w.notYet, v.Name)
		if err := w.declare("values."+v.Name, s.out); err != nil {
			return nil, errors.Wrapf(err, "model: declare value %q", v.Name)
		}
	}
	for i, p := range m.Spec.Parts {
		if err := w.part("spec.parts["+strconv.Itoa(i)+"]", p); err != nil {
			return nil, err
		}
	}
	inputs := make([]string, 0, len(m.Spec.Inputs))
	for name := range m.Spec.Inputs {
		inputs = append(inputs, name)
	}
	sort.Strings(inputs)
	return &Compiled{inputs: inputs, sites: w.sites}, nil
}

// siteSpec says how one site is read: strict is the exactly-one-hole form of values,
// when and forEach.in, loop and loopType declare the loop variable, and check tests the
// type of a new site before it is cached.
type siteSpec struct {
	strict   bool
	loop     string
	loopType *cel.Type
	check    func(out *cel.Type) error
}

// walker compiles the sites of one model. The CEL environment is built on the first site
// that is not in the cache, so a model that is fully cached builds none.
type walker struct {
	cache    *Cache
	key      string
	declared map[string]*cel.Type // the scope variables and the values so far
	notYet   map[string]bool      // the values that are not computed at this point
	env      *cel.Env
	sites    map[string]*site
}

// declare adds a variable to the scope of the sites that follow.
func (w *walker) declare(name string, t *cel.Type) error {
	w.declared[name] = t
	if w.env == nil {
		return nil
	}
	env, err := w.env.Extend(cel.Variable(name, t))
	if err != nil {
		return err
	}
	w.env = env
	return nil
}

func (w *walker) baseEnv() (*cel.Env, error) {
	if w.env == nil {
		env, err := newEnv(variables(w.declared)...)
		if err != nil {
			return nil, err
		}
		w.env = env
	}
	return w.env, nil
}

// site returns the compiled site at path, from the cache when its model identity, path
// and text are all there.
func (w *walker) site(path, text string, spec siteSpec) (*site, error) {
	if _, taken := w.sites[path]; taken {
		return nil, invalid(path, "two sites give the same path: a key that holds a dot or a bracket, or an integer key beside a string key of the same text, writes the path of another property")
	}
	k := cacheKey(w.key, path, text)
	s := w.cache.get(k)
	if s == nil {
		built, err := w.build(path, text, spec)
		if err != nil {
			return nil, err
		}
		s = w.cache.put(k, built)
	}
	w.sites[path] = s
	return s, nil
}

// cacheKey joins the model identity, the site path and the template text so that two different
// triples never make one key. The identity and the path carry their length, and the text is
// last. A separator byte would not do: a path and a text can hold any byte, a NUL included.
func cacheKey(identity, path, text string) string {
	return strconv.Itoa(len(identity)) + ":" + identity + strconv.Itoa(len(path)) + ":" + path + text
}

func (w *walker) build(path, text string, spec siteSpec) (*site, error) {
	env, err := w.baseEnv()
	if err != nil {
		return nil, err
	}
	if spec.loop != "" {
		if env, err = env.Extend(cel.Variable(spec.loop, spec.loopType)); err != nil {
			return nil, errors.Wrapf(err, "model: declare loop variable %q", spec.loop)
		}
	}
	var pieces []piece
	if spec.strict {
		inner, err := scanHole(text)
		if err != nil {
			return nil, invalid(path, "%s", err)
		}
		pieces = []piece{{text: inner, hole: true}}
	} else if pieces, err = splitTemplate(text); err != nil {
		return nil, invalid(path, "%s", err)
	}
	s := &site{single: len(pieces) == 1 && pieces[0].hole, loop: spec.loop, out: cel.StringType}
	for _, p := range pieces {
		if !p.hole {
			s.segs = append(s.segs, segment{text: p.text})
			continue
		}
		ast, out, err := checkHole(env, path, p.text, w.notYet, w.declared)
		if err != nil {
			return nil, err
		}
		switch {
		case s.single:
			if err := stringKeys(out); err != nil {
				return nil, invalid(path, "%s", err)
			}
		case !s.single && !hasText(out):
			return nil, invalid(path, "a %s hole is not allowed in text: it has no text form, so make the hole the whole value or convert it", out)
		}
		if s.single {
			s.out = out
		}
		prog, err := env.Program(ast, cel.CostLimit(maxExpressionCost), cel.CustomDecorator(compareDecorator), cel.CustomDecorator(chargeDecorator))
		if err != nil {
			return nil, invalid(path, "%s", err)
		}
		s.segs = append(s.segs, segment{prog: prog})
	}
	if spec.check != nil {
		if err := spec.check(s.out); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// part compiles the sites of one part.
func (w *walker) part(base string, p Part) error {
	if p.When != "" {
		_, err := w.site(base+".when", p.When, siteSpec{strict: true, check: func(out *cel.Type) error {
			if out.Kind() != types.BoolKind {
				return invalid(base+".when", "must give a bool, got %s", out)
			}
			return nil
		}})
		if err != nil {
			return err
		}
	}
	var spec siteSpec
	if p.ForEach != nil {
		in, err := w.site(base+".forEach.in", p.ForEach.In, siteSpec{strict: true, check: func(out *cel.Type) error {
			if out.Kind() != types.ListKind {
				return invalid(base+".forEach.in", "must give a list, got %s", out)
			}
			return nil
		}})
		if err != nil {
			return err
		}
		spec.loop, spec.loopType = p.ForEach.As, in.out.Parameters()[0]
	}
	if p.Name != "" {
		if _, err := w.site(base+".name", p.Name, spec); err != nil {
			return err
		}
	}
	if err := w.walkMap(base+".properties", p.Properties, spec); err != nil {
		return err
	}
	for j, t := range p.Traits {
		if err := w.walkMap(base+".traits["+strconv.Itoa(j)+"].properties", t.Properties, spec); err != nil {
			return err
		}
	}
	notes := make(map[string]any, len(p.Annotations))
	for k, v := range p.Annotations {
		notes[k] = v
	}
	return w.walkMap(base+".annotations", notes, spec)
}

// walkMap compiles the strings below a map, in sorted key order. A key is literal text: a
// `${` in one is refused, whether it is a hole or an escape.
func (w *walker) walkMap(path string, m map[string]any, spec siteSpec) error {
	entries := make([]entry, 0, len(m))
	for k, v := range m {
		entries = append(entries, entry{k, v})
	}
	return w.walkEntries(path, entries, spec)
}

// walkAnyMap is walkMap for a free-form map whose keys are not all strings, which yaml.v3 gives as
// map[any]any. The text of a key is its path segment, so the integer key 1 and the string key "1"
// are two entries with one path, and the second is refused as a path that is already taken.
func (w *walker) walkAnyMap(path string, m map[any]any, spec siteSpec) error {
	entries := make([]entry, 0, len(m))
	for k, v := range m {
		entries = append(entries, entry{keyText(k), v})
	}
	return w.walkEntries(path, entries, spec)
}

// entry is one key of a map, as the text of the key, with its value.
type entry struct {
	key string
	val any
}

// keyText is the text of a map key that is not a string: 1 gives "1", true gives "true", null gives "null".
func keyText(k any) string {
	if k == nil {
		return "null"
	}
	return fmt.Sprint(k)
}

func (w *walker) walkEntries(path string, entries []entry, spec siteSpec) error {
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	for _, e := range entries {
		kp := path + "." + e.key
		if strings.Contains(e.key, holeOpen) {
			return invalid(kp, "a %q in a map key is not allowed: a key is literal text, so neither a hole nor an escape works there", holeOpen)
		}
		if err := w.walk(kp, e.val, spec); err != nil {
			return err
		}
	}
	return nil
}

func (w *walker) walk(path string, v any, spec siteSpec) error {
	switch x := v.(type) {
	case string:
		_, err := w.site(path, x, spec)
		return err
	case map[string]any:
		return w.walkMap(path, x, spec)
	case map[any]any:
		return w.walkAnyMap(path, x, spec)
	case []any:
		for i, e := range x {
			if err := w.walk(path+"["+strconv.Itoa(i)+"]", e, spec); err != nil {
				return err
			}
		}
	}
	return nil
}

// piece is a run of literal text or the source of one hole.
type piece struct {
	text string
	hole bool
}

// splitTemplate cuts s into literal text and holes. `$${` writes a literal `${`. Any
// other `$` is text. The end of a hole is found as scanHole finds it.
func splitTemplate(s string) ([]piece, error) {
	var pieces []piece
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			pieces = append(pieces, piece{text: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "$"+holeOpen):
			lit.WriteString(holeOpen)
			i += 1 + len(holeOpen)
		case strings.HasPrefix(s[i:], holeOpen):
			end, err := holeEnd(s, i+len(holeOpen))
			if err != nil {
				return nil, err
			}
			inner := s[i+len(holeOpen) : end]
			if strings.TrimSpace(inner) == "" {
				return nil, errors.New("the ${ } hole is empty")
			}
			flush()
			pieces = append(pieces, piece{text: inner, hole: true})
			i = end + 1
		default:
			lit.WriteByte(s[i])
			i++
		}
	}
	flush()
	return pieces, nil
}

// hasText reports whether a value of type t has a text form inside mixed text.
func hasText(t *cel.Type) bool {
	switch t.Kind() {
	case types.StringKind, types.IntKind, types.UintKind, types.DoubleKind, types.BoolKind, types.NullTypeKind:
		return true
	default:
		return false
	}
}

// stringKeys refuses a map type whose keys are not strings, at any depth. A document
// has string keys only.
func stringKeys(t *cel.Type) error {
	if t.Kind() == types.MapKind {
		if p := t.Parameters(); len(p) == 2 && p[0].Kind() != types.StringKind && p[0].Kind() != types.DynKind {
			return errors.Errorf("map keys must be strings, got %s", p[0])
		}
	}
	for _, p := range t.Parameters() {
		if err := stringKeys(p); err != nil {
			return err
		}
	}
	return nil
}

// Eval runs the expression site named by path and returns the Go value and the actual
// cost. A single hole gives int64, uint64, float64, bool, string, []byte, []any,
// map[string]any or nil. A template with text gives a string. A failure names the site.
func (c *Compiled) Eval(path string, sc Scope) (any, int64, error) {
	s := c.sites[path]
	if s == nil {
		return nil, 0, errors.Errorf("model: %s is not an expression site", path)
	}
	act := make(map[string]any, len(c.inputs)+len(sc.Values)+4)
	for _, n := range c.inputs {
		act["inputs."+n] = sc.Inputs[n]
	}
	act["self.name"] = sc.Self.Name
	act["self.application"] = sc.Self.Application
	for n, v := range sc.Values {
		act["values."+n] = v
	}
	if s.loop != "" {
		act[s.loop] = sc.Loop
	}
	budget := newCompareBudget()
	act[compareBudgetName] = budget
	var cost int64
	var text strings.Builder
	for _, seg := range s.segs {
		if seg.prog == nil {
			text.WriteString(seg.text)
			continue
		}
		val, det, err := seg.prog.Eval(act)
		if n := det.ActualCost(); n != nil {
			cost += int64(min(*n, math.MaxInt64))
		}
		if budget.exhausted() {
			return nil, cost, siteError(path, compareStopError())
		}
		if err != nil {
			return nil, cost, siteError(path, err)
		}
		if s.single {
			out, err := toGo(val)
			if errors.Is(err, errResultSize) {
				return nil, cost, errors.Errorf("%w: %s: the result holds more than %d elements or bytes", ErrCostLimit, path, maxResultElements)
			}
			if err != nil {
				return nil, cost, errors.Errorf("model: evaluate %s: %w", path, err)
			}
			return out, cost, nil
		}
		t, err := textOf(val)
		if err != nil {
			return nil, cost, errors.Errorf("model: evaluate %s: %w", path, err)
		}
		text.WriteString(t)
	}
	return text.String(), cost, nil
}

// siteError names the site in an evaluation failure and marks a cost-limit stop.
func siteError(path string, err error) error {
	var stop interpreter.EvalCancelledError
	if errors.As(err, &stop) && stop.Cause == interpreter.CostLimitExceeded {
		if stop.Message == chargeStopMessage {
			return errors.Errorf("%w: %s: %s", ErrCostLimit, path, chargeStopMessage)
		}
		return errors.Errorf("%w: %s: more than %d", ErrCostLimit, path, maxExpressionCost)
	}
	return errors.Errorf("model: evaluate %s: %w", path, err)
}

// textOf is the text of a value inside mixed text. A double takes the form of CEL
// string(x): 1.0 gives "1" and 100000000.0 gives "1e+08". A null and a NaN or an
// infinity have no text.
func textOf(v ref.Val) (string, error) {
	switch x := v.(type) {
	case types.String:
		return string(x), nil
	case types.Int:
		return strconv.FormatInt(int64(x), 10), nil
	case types.Uint:
		return strconv.FormatUint(uint64(x), 10), nil
	case types.Bool:
		return strconv.FormatBool(bool(x)), nil
	case types.Double:
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return "", errors.Errorf("%w: %v", ErrNonFinite, f)
		}
		return strconv.FormatFloat(f, 'g', -1, 64), nil
	case types.Null:
		return "", errors.New("a null value has no text form: write the hole alone to keep null, or test it with == null")
	}
	return "", errors.Errorf("a %s value has no text form", v.Type())
}

// errResultSize stops the copy of a result that holds more than maxResultElements elements.
// Eval turns it into a failure that wraps ErrCostLimit and names the site.
var errResultSize = errors.New("model: result holds too many elements")

// converter copies CEL results to Go data and counts the list elements and map entries it makes.
type converter struct{ left int }

// take counts one element and reports errResultSize when the budget is spent.
func (c *converter) take() error {
	return c.takeN(1)
}

// takeN counts n units and reports errResultSize when the budget is spent.
func (c *converter) takeN(n int) error {
	c.left -= n
	if c.left < 0 {
		return errResultSize
	}
	return nil
}

// toGo converts a CEL result to plain Go data. Lists and maps convert at every depth, and
// together they hold at most maxResultElements elements, and each byte of a bytes value counts
// as one element. A map key must be a string, a double must be finite, and a bytes value is a
// copy.
func toGo(v ref.Val) (any, error) {
	c := converter{left: maxResultElements}
	return c.toGo(v)
}

func (c *converter) toGo(v ref.Val) (any, error) {
	switch x := v.(type) {
	case types.Null:
		return nil, nil
	case types.Bool:
		return bool(x), nil
	case types.Int:
		return int64(x), nil
	case types.Uint:
		return uint64(x), nil
	case types.Double:
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, errors.Errorf("%w: %v", ErrNonFinite, f)
		}
		return f, nil
	case types.String:
		if len(x) > maxValueSize {
			return nil, errors.Errorf("%w: a string holds more than %d bytes", ErrCostLimit, maxValueSize)
		}
		return string(x), nil
	case types.Bytes:
		if err := c.takeN(len(x)); err != nil {
			return nil, err
		}
		return append([]byte{}, x...), nil
	case traits.Mapper:
		out := map[string]any{}
		for it := x.Iterator(); it.HasNext() == types.True; {
			if err := c.take(); err != nil {
				return nil, err
			}
			k, ok := it.Next().(types.String)
			if !ok {
				return nil, errors.New("map keys must be strings")
			}
			if len(k) > maxValueSize {
				return nil, errors.Errorf("%w: a map key holds more than %d bytes", ErrCostLimit, maxValueSize)
			}
			e, err := c.toGo(x.Get(k))
			if err != nil {
				return nil, err
			}
			out[string(k)] = e
		}
		return out, nil
	case traits.Lister:
		out := []any{}
		for it := x.Iterator(); it.HasNext() == types.True; {
			if err := c.take(); err != nil {
				return nil, err
			}
			e, err := c.toGo(it.Next())
			if err != nil {
				return nil, err
			}
			out = append(out, e)
		}
		return out, nil
	}
	return nil, errors.Errorf("a %s value has no place in a document", v.Type())
}

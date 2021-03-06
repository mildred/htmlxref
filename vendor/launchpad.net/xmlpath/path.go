package xmlpath

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Path is a compiled path that can be applied to a context
// node to obtain a matching node set.
// A single Path can be applied concurrently to any number
// of context nodes.
type Path struct {
	path       string
	steps      []pathStep
	namespaces map[string]string
}

// Iter returns an iterator that goes over the list of nodes
// that p matches on the given context.
func (p *Path) Iter(context *Node) *Iter {
	iter := Iter{
		make([]pathStepState, len(p.steps)),
		make([]bool, len(context.nodes)),
	}
	for i := range p.steps {
		iter.state[i].step = &p.steps[i]
	}
	iter.state[0].init(context)
	return &iter
}

// Exists returns whether any nodes match p on the given context.
func (p *Path) Exists(context *Node) bool {
	return p.Iter(context).Next()
}

// String returns the string value of the first node matched
// by p on the given context.
//
// See the documentation of Node.String.
func (p *Path) String(context *Node) (s string, ok bool) {
	iter := p.Iter(context)
	if iter.Next() {
		return iter.Node().String(), true
	}
	return "", false
}

// Bytes returns as a byte slice the string value of the first
// node matched by p on the given context.
//
// See the documentation of Node.String.
func (p *Path) Bytes(node *Node) (b []byte, ok bool) {
	iter := p.Iter(node)
	if iter.Next() {
		return iter.Node().Bytes(), true
	}
	return nil, false
}

// Iter iterates over node sets.
// The DOM must not be modified during the iteration
type Iter struct {
	state []pathStepState
	seen  []bool
}

// In case you plan to modify the DOM
func (iter *Iter) Nodes() []*NodeRef {
	var res []*NodeRef
	for iter.Next() {
		res = append(res, iter.Node().Ref)
	}
	return res
}

// Node returns the current node.
// Must only be called after Iter.Next returns true.
func (iter *Iter) Node() *Node {
	state := iter.state[len(iter.state)-1]
	if state.pos == 0 {
		panic("Iter.Node called before Iter.Next")
	}
	if state.node == nil {
		panic("Iter.Node called after Iter.Next false")
	}
	return state.node
}

// Next iterates to the next node in the set, if any, and
// returns whether there is a node available.
func (iter *Iter) Next() bool {
	tip := len(iter.state) - 1
outer:
	for {
		for !iter.state[tip].next() {
			tip--
			if tip == -1 {
				return false
			}
		}
		for tip < len(iter.state)-1 {
			tip++
			iter.state[tip].init(iter.state[tip-1].node)
			if !iter.state[tip].next() {
				tip--
				continue outer
			}
		}
		if iter.seen[iter.state[tip].node.pos] {
			continue
		}
		iter.seen[iter.state[tip].node.pos] = true
		return true
	}
	panic("unreachable")
}

type pathStepState struct {
	step *pathStep
	node *Node
	pos  int
	idx  int
	aux  int
}

func (s *pathStepState) init(node *Node) {
	s.node = node
	s.pos = 0
	s.idx = 0
	s.aux = 0
}

func (s *pathStepState) next() bool {
	for s._next() {
		s.pos++
		if s.step.pred == nil {
			return true
		}
		if s.step.pred.Eval(s.node, s.pos) {
			return true
		}
	}
	return false
}

func (s *pathStepState) _next() bool {
	if s.node == nil {
		return false
	}
	if s.step.root && s.idx == 0 {
		for s.node.up != nil {
			s.node = s.node.up
		}
	}

	if s.aux >= len(s.node.nodes) {
		panic("s.aux out of range")
	}

	switch s.step.axis {

	case "self":
		if s.idx == 0 && s.step.match(s.node) {
			s.idx++
			return true
		}

	case "parent":
		if s.idx == 0 && s.node.up != nil && s.step.match(s.node.up) {
			s.idx++
			s.node = s.node.up
			return true
		}

	case "ancestor", "ancestor-or-self":
		if s.idx == 0 && s.step.axis == "ancestor-or-self" {
			s.idx++
			if s.step.match(s.node) {
				return true
			}
		}
		for s.node.up != nil {
			s.node = s.node.up
			s.idx++
			if s.step.match(s.node) {
				return true
			}
		}

	case "child":
		var down []*Node
		if s.idx == 0 {
			down = s.node.down
		} else {
			down = s.node.up.down
		}
		for s.idx < len(down) {
			node := down[s.idx]
			s.idx++
			if s.step.match(node) {
				s.node = node
				return true
			}
		}

	case "descendant", "descendant-or-self":
		if s.idx == 0 {
			s.idx = s.node.pos
			s.aux = s.node.end
			if s.step.axis == "descendant" {
				s.idx++
			}
		}
		for s.idx < s.aux {
			node := &s.node.nodes[s.idx]
			s.idx++
			if node.kind == AttrNode {
				continue
			}
			if s.step.match(node) {
				s.node = node
				return true
			}
		}

	case "following":
		if s.idx == 0 {
			s.idx = s.node.end
		}
		for s.idx < len(s.node.nodes) {
			node := &s.node.nodes[s.idx]
			s.idx++
			if node.kind == AttrNode {
				continue
			}
			if s.step.match(node) {
				s.node = node
				return true
			}
		}

	case "following-sibling":
		var down []*Node
		if s.node.up != nil {
			down = s.node.up.down
			if s.idx == 0 {
				for s.idx < len(down) {
					node := down[s.idx]
					s.idx++
					if node == s.node {
						break
					}
				}
			}
		}
		for s.idx < len(down) {
			node := down[s.idx]
			s.idx++
			if s.step.match(node) {
				s.node = node
				return true
			}
		}

	case "preceding":
		if s.idx == 0 {
			s.aux = s.node.pos // Detect ancestors.
			s.idx = s.node.pos - 1
		}
		for s.idx >= 0 {
			node := &s.node.nodes[s.idx]
			s.idx--
			if node.kind == AttrNode {
				continue
			}
			if node == s.node.nodes[s.aux].up {
				s.aux = s.node.nodes[s.aux].up.pos
				continue
			}
			if s.step.match(node) {
				s.node = node
				return true
			}
		}

	case "preceding-sibling":
		var down []*Node
		if s.node.up != nil {
			down = s.node.up.down
			if s.aux == 0 {
				s.aux = 1
				for s.idx < len(down) {
					node := down[s.idx]
					s.idx++
					if node == s.node {
						s.idx--
						break
					}
				}
			}
		}
		for s.idx >= 0 {
			node := down[s.idx]
			s.idx--
			if s.step.match(node) {
				s.node = node
				return true
			}
		}

	case "attribute":
		if s.idx == 0 {
			s.idx = s.node.pos + 1
			s.aux = s.node.end
		}
		for s.idx < s.aux {
			node := &s.node.nodes[s.idx]
			s.idx++
			if node.kind != AttrNode {
				break
			}
			if s.step.match(node) {
				s.node = node
				return true
			}
		}

	}

	s.node = nil
	return false
}

type expr interface {
	Eval(node *Node, pos int) bool
}

type exprOpEq struct {
	lval *Path
	rval string
}

func (e *exprOpEq) Eval(node *Node, pos int) bool {
	iter := e.lval.Iter(node)
	for iter.Next() {
		if iter.Node().equals(e.rval) {
			return true
		}
	}
	return false
}

type exprOpOr struct {
	vals []expr
}

func (e *exprOpOr) Eval(node *Node, pos int) bool {
	for _, e := range e.vals {
		res := e.Eval(node, pos)
		if res {
			return true
		}
	}
	return false
}

type exprOpAnd struct {
	vals []expr
}

func (e *exprOpAnd) Eval(node *Node, pos int) bool {
	for _, e := range e.vals {
		res := e.Eval(node, pos)
		if !res {
			return false
		}
	}
	return true
}

type exprString struct {
	val string
}

type exprInt struct {
	val int
}

func (e *exprInt) Eval(node *Node, pos int) bool {
	return e.val == pos
}

type exprBool struct {
	val bool
}

func (e *exprBool) Eval(node *Node, pos int) bool {
	return e.val
}

type exprPath struct {
	path *Path
}

func (e *exprPath) Eval(node *Node, pos int) bool {
	return e.path.Exists(node)
}

type pathStep struct {
	root   bool
	axis   string
	prefix string
	space  string
	name   string
	kind   NodeKind
	pred   expr
}

func (step *pathStep) match(node *Node) bool {
	return node.kind != EndNode &&
		(step.kind == AnyNode || step.kind == node.kind) &&
		(step.name == "*" || (node.name.Local == step.name && node.name.Space == step.space))
}

// MustCompile returns the compiled path, and panics if
// there are any errors.
func MustCompile(path string) *Path {
	return MustCompileNS(path, nil)
}

func MustCompileNS(path string, ns map[string]string) *Path {
	e, err := CompileNS(path, ns)
	if err != nil {
		panic(err)
	}
	return e
}

// Compile returns the compiled path.
func Compile(path string) (*Path, error) {
	return CompileNS(path, nil)
}

func CompileNS(path string, ns map[string]string) (*Path, error) {
	c := pathCompiler{path, 0}
	if path == "" {
		return nil, c.errorf("empty path")
	}
	if ns == nil {
		ns = map[string]string{}
	}
	if _, ok := ns[""]; !ok {
		ns[""] = ""
	}
	ns["xml"] = "http://www.w3.org/XML/1998/namespace"
	p, err := c.parsePath(ns) // TODO: parse function calls before
	if err != nil {
		return nil, err
	}
	return p, nil
}

type pathCompiler struct {
	path string
	i    int
}

func (c *pathCompiler) errorf(format string, args ...interface{}) error {
	return fmt.Errorf("compiling xml path %q:%d: %s", c.path, c.i, fmt.Sprintf(format, args...))
}

func (c *pathCompiler) parsePath(ns map[string]string) (path *Path, err error) {
	var steps []pathStep
	var start = c.i
	for {
		step := pathStep{axis: "child", prefix: ""}

		if c.i == 0 && c.skipByte('/') {
			step.root = true
			if len(c.path) == 1 {
				step.name = "*"
			}
		}
		if c.peekByte('/') {
			step.axis = "descendant-or-self"
			step.name = "*"
		} else if c.skipByte('@') {
			mark := c.i
			if !c.skipName() {
				return nil, c.errorf("missing name after @")
			}
			step.axis = "attribute"
			step.prefix, step.name = extractPrefix(c.path[mark:c.i])
			step.kind = AttrNode
		} else {
			mark := c.i
			if c.skipName() {
				step.name = c.path[mark:c.i]
			}
			if step.name == "" {
				return nil, c.errorf("missing name")
			} else if step.name == "*" {
				step.kind = StartNode
			} else if step.name == "." {
				step.axis = "self"
				step.name = "*"
			} else if step.name == ".." {
				step.axis = "parent"
				step.name = "*"
			} else {
				if c.skipByte(':') {
					if !c.skipByte(':') {
						return nil, c.errorf("missing ':'")
					}
					switch step.name {
					case "attribute":
						step.kind = AttrNode
					case "self", "child", "parent":
					case "descendant", "descendant-or-self":
					case "ancestor", "ancestor-or-self":
					case "following", "following-sibling":
					case "preceding", "preceding-sibling":
					default:
						return nil, c.errorf("unsupported axis: %q", step.name)
					}
					step.axis = step.name

					mark = c.i
					if !c.skipName() {
						return nil, c.errorf("missing name")
					}
					step.name = c.path[mark:c.i]
				}
				if c.skipByte('(') {
					conflict := step.kind != AnyNode
					switch step.name {
					case "node":
						// must be AnyNode
					case "text":
						step.kind = TextNode
					case "comment":
						step.kind = CommentNode
					case "processing-instruction":
						step.kind = ProcInstNode
					default:
						return nil, c.errorf("unsupported expression: %s()", step.name)
					}
					if conflict {
						return nil, c.errorf("%s() cannot succeed on axis %q", step.name, step.axis)
					}

					literal, err := c.parseLiteral()
					if err == errNoLiteral {
						step.name = "*"
					} else if err != nil {
						return nil, c.errorf("%v", err)
					} else if step.kind == ProcInstNode {
						step.name = literal
					} else {
						return nil, c.errorf("%s() has no arguments", step.name)
					}
					if !c.skipByte(')') {
						return nil, c.errorf("missing )")
					}
				} else if step.name == "*" && step.kind == AnyNode {
					step.kind = StartNode
				}
			}
			step.prefix, step.name = extractPrefix(step.name)
		}
		step.space = ns[step.prefix]
		if c.skipByte('[') {
			step.pred, err = c.parseExpr(ns)
			if err != nil {
				return nil, err
			}
			if !c.skipByte(']') {
				return nil, c.errorf("expected ']'")
			}
		}
		steps = append(steps, step)
		//fmt.Printf("step: %#v\n", step)
		if !c.skipByte('/') {
			if (start == 0 || start == c.i) && c.i < len(c.path) {
				return nil, c.errorf("unexpected %q", c.path[c.i])
			}
			return &Path{steps: steps, path: c.path[start:c.i], namespaces: ns}, nil
		}
	}
	panic("unreachable")
}

func (c *pathCompiler) parseExpr(ns map[string]string) (pred expr, err error) {
	return c.parseOrExpr(ns)
}

func (c *pathCompiler) parseOrExpr(ns map[string]string) (pred expr, err error) {
	c.skipSpaces()
	lval, err := c.parseAndExpr(ns)
	if err != nil {
		return nil, err
	}
	expr := &exprOpOr{vals: []expr{lval}}
	pred = expr

	for {
		c.skipSpaces()
		i := c.i
		if !c.skipString("or") || !c.skipSpaces() {
			c.i = i
			if len(expr.vals) == 1 {
				return lval, nil
			} else {
				return pred, nil
			}
		}

		rval, err := c.parseAndExpr(ns)
		if err != nil {
			return nil, err
		}
		expr.vals = append(expr.vals, rval)
	}
}

func (c *pathCompiler) parseAndExpr(ns map[string]string) (pred expr, err error) {
	c.skipSpaces()
	lval, err := c.parseExprLeaf(ns)
	if err != nil {
		return nil, err
	}
	expr := &exprOpAnd{vals: []expr{lval}}
	pred = expr

	for {
		c.skipSpaces()
		i := c.i
		if !c.skipString("and") || !c.skipSpaces() {
			c.i = i
			if len(expr.vals) == 1 {
				return lval, nil
			} else {
				return pred, nil
			}
		}

		rval, err := c.parseExprLeaf(ns)
		if err != nil {
			return nil, err
		}
		expr.vals = append(expr.vals, rval)
	}
}

func (c *pathCompiler) parseExprLeaf(ns map[string]string) (pred expr, err error) {
	pred = &exprBool{false}
	if ival, ok := c.parseInt(); ok {
		if ival == 0 {
			return nil, c.errorf("positions start at 1")
		}
		pred = &exprInt{ival}
	} else {
		path, err := c.parsePath(ns) // should include function expressions
		if err != nil {
			return nil, err
		}
		if path.path[0] == '-' {
			if _, err = strconv.Atoi(path.path); err == nil {
				return nil, c.errorf("positions must be positive")
			}
		}
		if c.skipByte('=') {
			// TODO: here rval should be a generic path (including function calls)
			sval, err := c.parseLiteral()
			if err != nil {
				return nil, c.errorf("%v", err)
			}
			pred = &exprOpEq{path, sval} // TODO: sval should be rval, a path expr
		} else {
			pred = &exprPath{path}
		}
	}
	// TODO: support boolean operators
	return pred, nil
}

func extractPrefix(fullname string) (string, string) {
	i := strings.Index(fullname, ":")
	if i == -1 || i == len(fullname)-1 {
		return "", fullname
	} else {
		return fullname[:i], fullname[i+1:]
	}
}

var errNoLiteral = fmt.Errorf("expected a literal string")

func (c *pathCompiler) parseLiteral() (string, error) {
	if c.skipByte('"') {
		mark := c.i
		if !c.skipByteFind('"') {
			return "", fmt.Errorf(`missing '"'`)
		}
		return c.path[mark : c.i-1], nil
	}
	if c.skipByte('\'') {
		mark := c.i
		if !c.skipByteFind('\'') {
			return "", fmt.Errorf(`missing "'"`)
		}
		return c.path[mark : c.i-1], nil
	}
	return "", errNoLiteral
}

func (c *pathCompiler) parseInt() (v int, ok bool) {
	mark := c.i
	for c.i < len(c.path) && c.path[c.i] >= '0' && c.path[c.i] <= '9' {
		v *= 10
		v += int(c.path[c.i]) - '0'
		c.i++
	}
	if c.i == mark {
		return 0, false
	}
	return v, true
}

func (c *pathCompiler) skipSpaces() bool {
	res := false
	for c.i < len(c.path) && strings.ContainsAny(string(c.path[c.i]), " \t\n\v") {
		c.i++
		res = true
	}
	return res
}

func (c *pathCompiler) skipString(str string) bool {
	if c.i+len(str)-1 < len(c.path) && c.path[c.i:c.i+len(str)] == str {
		c.i += len(str)
		return true
	}
	return false
}

func (c *pathCompiler) skipByte(b byte) bool {
	if c.i < len(c.path) && c.path[c.i] == b {
		c.i++
		return true
	}
	return false
}

func (c *pathCompiler) skipByteFind(b byte) bool {
	for i := c.i; i < len(c.path); i++ {
		if c.path[i] == b {
			c.i = i + 1
			return true
		}
	}
	return false
}

func (c *pathCompiler) peekByte(b byte) bool {
	return c.i < len(c.path) && c.path[c.i] == b
}

// Peek the Nth byte or return '\0'
// N=1 means the next byte
func (c *pathCompiler) peekN(offset int) byte {
	i := c.i + offset - 1
	if i >= len(c.path) {
		return 0
	} else {
		return c.path[i]
	}
}

func (c *pathCompiler) skipName() bool {
	if c.i >= len(c.path) {
		return false
	}
	if c.path[c.i] == '*' {
		c.i++
		return true
	}
	start := c.i
	for c.i < len(c.path) && (c.path[c.i] >= utf8.RuneSelf || isNameByte(c.path[c.i])) {
		c.i++
	}
	// Allow namespace separator once
	if c.peekN(1) == ':' && (c.peekN(2) >= utf8.RuneSelf || isNameByte(c.peekN(2))) {
		c.i++
		for c.i < len(c.path) && (c.path[c.i] >= utf8.RuneSelf || isNameByte(c.path[c.i])) {
			c.i++
		}
	}
	return c.i > start
}

func isNameByte(c byte) bool {
	return 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '_' || c == '.' || c == '-'
}

package prompt

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/template"
	"text/template/parse"
	"unicode/utf8"
)

// The admitted dialect.
//
// An entry is text/template syntax, restricted until its variable contract is
// decidable by reading it:
//
//   - text, comments and trim markers;
//   - `{{.Name}}`, a declared variable, one level deep;
//   - `{{if}}`, `{{else if}}`, `{{else}}`, `{{end}}`;
//   - `and`, `or`, `not` over variables, string literals and parenthesised
//     expressions, and `eq`, `ne` over variables and string literals.
//
// Everything else is refused: `range` and `with` rebind dot, so `.Name` stops
// meaning a declared variable; `define`, `template` and `block` make an entry
// more than one text; `$x :=` declarations and `|` pipelines add nothing a
// string-valued contract needs; and the remaining builtins either fail on
// strings or format output the model then receives.
//
// Narrow on purpose, because only one direction is cheap. Entries are
// persisted under a content identity and validated against the harness that
// runs them (ADR 0031 section 5), so widening the dialect later leaves every
// stored pack valid, while narrowing it would strand packs already installed.
// The items that register the first production slots (design D11) widen it
// when a real entry needs more, and variables stay strings until one does.
//
// The constructs are chosen to be total over string values, so that an entry
// passing the static walk does not fail at render on a branch the
// import-time render happened not to take. That is argued from
// text/template's semantics and exercised construct by construct in
// TestAdmittedDialectRendersOnEveryPath; it is not proven, and anything
// admitted later owes the same argument.

// parseFuncs names every text/template builtin. The parser needs only the
// names, to tell a function from a typo; the walk below then refuses all but
// the admitted five with a message that says why. A builtin added to Go and
// missing here fails as ErrParse instead of ErrDialect: refused either way.
func parseFuncs() map[string]any {
	names := []string{
		"and", "call", "eq", "ge", "gt", "html", "index", "js", "le", "len", "lt",
		"ne", "not", "or", "print", "printf", "println", "slice", "urlquery",
	}
	funcs := make(map[string]any, len(names))
	for _, name := range names {
		// Any non-nil value: the parser tests the entry against nil and
		// never calls it.
		funcs[name] = true
	}
	return funcs
}

// Argument rules for the admitted functions. Boolean connectives take
// anything the dialect can express; comparisons take only strings, because
// `eq .A (not .B)` compares a string with a bool and fails at render.
const (
	funcAnd = "and"
	funcOr  = "or"
	funcNot = "not"
	funcEq  = "eq"
	funcNe  = "ne"
)

// checkEntryText refuses text that cannot be an entry whatever it says.
//
// Invalid UTF-8 and NUL are refused because the pack's identity is a digest
// over a JSON object holding this text, hashed "byte for byte" (ADR 0031
// section 1). json.Marshal replaces each invalid byte with U+FFFD, so two
// different entries would share one digest; and jsonb, where the entries are
// stored, cannot hold U+0000 at all.
func checkEntryText(slot SlotKey, text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("%w: slot %q is blank, which supplies the slot with nothing", ErrInvalidEntry, slot)
	}
	if !utf8.ValidString(text) {
		return fmt.Errorf("%w: slot %q is not valid UTF-8, so its digest would not cover its bytes", ErrInvalidEntry, slot)
	}
	if strings.ContainsRune(text, 0) {
		return fmt.Errorf("%w: slot %q contains a NUL character, which the plane cannot store", ErrInvalidEntry, slot)
	}
	return nil
}

// parseEntry parses one entry and proves it is inside the dialect and inside
// its slot's variable contract. It returns a tree ready to execute.
func parseEntry(key SlotKey, slot Slot, text string) (*parse.Tree, error) {
	if err := checkEntryText(key, text); err != nil {
		return nil, err
	}

	// parse.Tree directly, not template.Parse. The latter looks the result
	// up by name in the set of trees the text defined, so an entry that is
	// nothing but `{{define "<its own slot key>"}}...{{end}}` comes back as
	// an ordinary root tree with no define node left in it to refuse. Here
	// the returned tree IS the top-level text, and any other tree in the set
	// is a definition.
	trees := make(map[string]*parse.Tree)
	top, err := parse.New(string(key)).Parse(text, "", "", trees, parseFuncs())
	if err != nil {
		return nil, fmt.Errorf("%w: slot %q: %w", ErrParse, key, err)
	}
	for _, name := range slices.Sorted(maps.Keys(trees)) {
		if trees[name] != top {
			return nil, fmt.Errorf("%w: slot %q defines template %q; an entry is one text", ErrDialect, key, name)
		}
	}

	w := walker{key: key, slot: slot}
	if err := w.list(top.Root); err != nil {
		return nil, err
	}
	return top, nil
}

// walker carries what a refusal needs to name.
type walker struct {
	key  SlotKey
	slot Slot
}

func (w walker) refuse(node parse.Node, why string) error {
	return fmt.Errorf("%w: slot %q: %s: %s", ErrDialect, w.key, why, node.String())
}

func (w walker) list(list *parse.ListNode) error {
	if list == nil {
		return nil
	}
	for _, node := range list.Nodes {
		if err := w.node(node); err != nil {
			return err
		}
	}
	return nil
}

func (w walker) node(node parse.Node) error {
	switch typed := node.(type) {
	case *parse.TextNode, *parse.CommentNode:
		return nil
	case *parse.ActionNode:
		return w.pipe(typed.Pipe)
	case *parse.IfNode:
		if err := w.pipe(typed.Pipe); err != nil {
			return err
		}
		if err := w.list(typed.List); err != nil {
			return err
		}
		// `else if` parses as an IfNode nested in ElseList, so it needs no
		// case of its own.
		return w.list(typed.ElseList)
	case *parse.RangeNode, *parse.WithNode:
		return w.refuse(node, "range and with rebind dot, so a variable reference stops being one")
	case *parse.TemplateNode:
		return w.refuse(node, "an entry is one text and may not invoke another")
	default:
		// Deny by default: a node type added to text/template is outside
		// the dialect until somebody admits it here.
		return w.refuse(node, "construct is not admitted")
	}
}

func (w walker) pipe(pipe *parse.PipeNode) error {
	if len(pipe.Decl) != 0 {
		return w.refuse(pipe, "variable declarations are not admitted")
	}
	if len(pipe.Cmds) != 1 {
		// A piped value arrives as the next command's final argument, so
		// admitting `|` would mean a second set of arity rules for no
		// expression the prefix form cannot write.
		return w.refuse(pipe, "pipelines are not admitted; write the function first")
	}
	return w.command(pipe.Cmds[0])
}

func (w walker) command(cmd *parse.CommandNode) error {
	function, isCall := cmd.Args[0].(*parse.IdentifierNode)
	if !isCall {
		if len(cmd.Args) != 1 {
			return w.refuse(cmd, "only a function takes arguments")
		}
		return w.operand(cmd.Args[0], true)
	}

	args := cmd.Args[1:]
	var arityOK, nested bool
	switch function.Ident {
	case funcAnd, funcOr:
		arityOK, nested = len(args) >= 1, true
	case funcNot:
		arityOK, nested = len(args) == 1, true
	case funcEq:
		arityOK = len(args) >= 2
	case funcNe:
		arityOK = len(args) == 2
	default:
		return w.refuse(cmd, fmt.Sprintf("function %q is not admitted (admitted: %s, %s, %s, %s, %s)",
			function.Ident, funcAnd, funcOr, funcNot, funcEq, funcNe))
	}
	if !arityOK {
		return w.refuse(cmd, fmt.Sprintf("wrong number of arguments for %q", function.Ident))
	}
	for _, arg := range args {
		if err := w.operand(arg, nested); err != nil {
			return err
		}
	}
	return nil
}

// operand admits a variable, a string literal, and -- where the caller can
// take a non-string -- a parenthesised expression.
func (w walker) operand(node parse.Node, nested bool) error {
	switch typed := node.(type) {
	case *parse.FieldNode:
		if len(typed.Ident) != 1 {
			return w.refuse(node, "variables are one level deep")
		}
		name := Variable(typed.Ident[0])
		if !slices.Contains(w.slot.Variables, name) {
			return fmt.Errorf("%w: slot %q references %q (supplied: %v)",
				ErrUndeclaredVariable, w.key, name, w.slot.Variables)
		}
		return nil
	case *parse.StringNode:
		return nil
	case *parse.PipeNode:
		if !nested {
			return w.refuse(node, "a comparison takes variables and string literals only")
		}
		return w.pipe(typed)
	default:
		return w.refuse(node, "operand is not a declared variable or a string literal")
	}
}

// execute runs a checked tree over exactly the slot's variables.
func execute(key SlotKey, tree *parse.Tree, values map[string]string) (string, error) {
	// missingkey=error so that a reference the walk somehow missed fails
	// loudly rather than rendering "<no value>" into a prompt.
	runner, err := template.New(string(key)).Option("missingkey=error").AddParseTree(string(key), tree)
	if err != nil {
		return "", fmt.Errorf("%w: slot %q: %w", ErrRender, key, err)
	}
	var out strings.Builder
	if err := runner.Execute(&out, values); err != nil {
		return "", fmt.Errorf("%w: slot %q: %w", ErrRender, key, err)
	}
	return out.String(), nil
}

// Render renders one slot's entry over the values the harness supplies.
//
// The values must be exactly the slot's declared variables. A missing one
// means the registration promised something the call site does not deliver;
// an extra one means the call site supplies something no entry was ever
// allowed to use. Both are harness defects, and refusing them here is what
// keeps the registration an accurate statement of what import validated
// against.
//
// The entry is re-checked rather than trusted: resolution hands the harness
// text that was validated at install, possibly by an older harness, and the
// check is cheap beside a model call.
func (r *Registry) Render(key SlotKey, entry string, values map[Variable]string) (string, error) {
	slot, ok := r.slots[key]
	if !ok {
		return "", fmt.Errorf("%w: %q (registered: %v)", ErrUnknownSlot, key, r.Slots())
	}
	supplied := slices.Sorted(maps.Keys(values))
	if declared := slices.Sorted(slices.Values(slot.Variables)); !slices.Equal(supplied, declared) {
		return "", fmt.Errorf("%w: slot %q declares %v and was given %v", ErrVariableSet, key, declared, supplied)
	}
	tree, err := parseEntry(key, slot, entry)
	if err != nil {
		return "", err
	}
	data := make(map[string]string, len(values))
	for name, value := range values {
		data[string(name)] = value
	}
	return execute(key, tree, data)
}

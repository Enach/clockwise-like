package api

// PAC-21, AC-3 (contract half): every 5xx response in the bundle is the JSON
// {error: string} envelope, and no 5xx description says the body carries a raw
// error. The bundle is read from the repository (contracts/openapi), so this
// test needs the whole repository, not only backend/, to be visible:
// scripts/test-backend.sh mounts the repository root for that reason.

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const pac21Bundle = "../../contracts/openapi/openapi.yaml"

// yamlNode is the subset of YAML the bundle uses where this test looks:
// block mappings of block mappings and scalars. yaml.safe_dump (the
// assembler) writes nothing else in paths/*/responses and components; lists
// are skipped, and a scalar's continuation lines are joined to it.
type yamlNode struct {
	children map[string]*yamlNode
	order    []string
	scalar   string
}

func (n *yamlNode) get(path ...string) *yamlNode {
	cur := n
	for _, k := range path {
		if cur == nil || cur.children == nil {
			return nil
		}
		cur = cur.children[k]
	}
	return cur
}

func yamlKey(content string) (key, rest string, ok bool) {
	if content == "" {
		return "", "", false
	}
	if q := content[0]; q == '\'' || q == '"' {
		end := strings.IndexByte(content[1:], q)
		if end < 0 {
			return "", "", false
		}
		key = content[1 : end+1]
		after := content[end+2:]
		if !strings.HasPrefix(after, ":") {
			return "", "", false
		}
		return key, strings.TrimSpace(after[1:]), true
	}
	if strings.HasSuffix(content, ":") && !strings.Contains(content, ": ") {
		return strings.TrimSuffix(content, ":"), "", true
	}
	i := strings.Index(content, ": ")
	if i < 0 {
		return "", "", false
	}
	return content[:i], strings.TrimSpace(content[i+2:]), true
}

func parseBundle(t *testing.T, path string) *yamlNode {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		abs, _ := filepath.Abs(path)
		t.Fatalf("open the contract bundle %s: %v (the test needs the repository root mounted, see scripts/test-backend.sh)", abs, err)
	}
	defer f.Close()

	type frame struct {
		indent int
		node   *yamlNode
	}
	root := &yamlNode{children: map[string]*yamlNode{}}
	stack := []frame{{-1, root}}
	skipIndent := -1   // lines indented deeper than this belong to the previous scalar or list
	var cont *yamlNode // the scalar continuation lines are appended to
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		content := strings.TrimLeft(line, " ")
		if content == "" || strings.HasPrefix(content, "#") {
			continue
		}
		indent := len(line) - len(content)
		if skipIndent >= 0 && (indent > skipIndent || (indent == skipIndent && strings.HasPrefix(content, "- "))) {
			if cont != nil {
				cont.scalar += " " + content
			}
			continue
		}
		skipIndent, cont = -1, nil
		if strings.HasPrefix(content, "- ") || content == "-" {
			skipIndent = indent
			continue
		}
		key, rest, ok := yamlKey(content)
		if !ok {
			t.Fatalf("bundle line not understood by the test parser: %q", line)
		}
		for len(stack) > 1 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		parent := stack[len(stack)-1].node
		child := &yamlNode{}
		if parent.children == nil {
			parent.children = map[string]*yamlNode{}
		}
		parent.children[key] = child
		parent.order = append(parent.order, key)
		if rest == "" {
			child.children = map[string]*yamlNode{}
			stack = append(stack, frame{indent, child})
			continue
		}
		child.scalar = rest
		skipIndent, cont = indent, child
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return root
}

func refName(n *yamlNode, prefix string) string {
	if n == nil || n.get("$ref") == nil {
		return ""
	}
	ref := strings.Trim(n.get("$ref").scalar, `'"`)
	if !strings.HasPrefix(ref, prefix) {
		return ""
	}
	return strings.TrimPrefix(ref, prefix)
}

// isErrorEnvelope: an object whose only property is a string `error`.
func isErrorEnvelope(schema *yamlNode) bool {
	if schema == nil {
		return false
	}
	props := schema.get("properties")
	if props == nil || len(props.order) != 1 || props.order[0] != "error" {
		return false
	}
	typ := props.get("error", "type")
	return typ != nil && strings.Trim(typ.scalar, `'"`) == "string"
}

var rawErrorProse = regexp.MustCompile(`(?i)<err>|err\.Error|raw (Go )?err|verbatim|underlying error|driver error`)

func check5xxResponse(t *testing.T, root *yamlNode, where string, resp *yamlNode) []string {
	var out []string
	if name := refName(resp, "#/components/responses/"); name != "" {
		resp = root.get("components", "responses", name)
		where += " -> components/responses/" + name
		if resp == nil {
			return []string{where + ": dangling $ref"}
		}
	}
	if d := resp.get("description"); d != nil && rawErrorProse.MatchString(d.scalar) {
		out = append(out, where+": description says the body carries a raw error: "+rawErrorProse.FindString(d.scalar))
	}
	content := resp.get("content")
	if content == nil || len(content.order) == 0 {
		return append(out, where+": no content (a 5xx must document the JSON envelope)")
	}
	for _, mt := range content.order {
		if mt != "application/json" {
			out = append(out, where+": documents "+mt+", not application/json")
			continue
		}
		schema := content.get(mt, "schema")
		if name := refName(schema, "#/components/schemas/"); name != "" {
			schema = root.get("components", "schemas", name)
			if !isErrorEnvelope(schema) {
				out = append(out, where+": schema "+name+" is not an {error: string} envelope")
			}
			continue
		}
		if !isErrorEnvelope(schema) {
			out = append(out, where+": inline schema is not an {error: string} envelope")
		}
	}
	return out
}

func TestPAC21Every5xxInTheContractIsTheJSONEnvelope(t *testing.T) {
	root := parseBundle(t, pac21Bundle)
	paths := root.get("paths")
	if paths == nil || len(paths.order) < 90 {
		t.Fatal("the bundle parsed to fewer than 90 paths; the test parser is not reading it")
	}
	var violations []string
	n5xx := 0
	for _, p := range paths.order {
		for _, method := range paths.get(p).order {
			responses := paths.get(p, method, "responses")
			if responses == nil {
				continue
			}
			for _, code := range responses.order {
				if !strings.HasPrefix(code, "5") {
					continue
				}
				n5xx++
				violations = append(violations, check5xxResponse(t, root, method+" "+p+" "+code, responses.get(code))...)
			}
		}
	}
	if n5xx < 100 {
		t.Fatalf("found only %d 5xx responses; the test parser is not reading the bundle", n5xx)
	}
	sort.Strings(violations)
	if len(violations) > 0 {
		t.Fatalf("%d of %d 5xx responses break the PAC-21 envelope rule:\n%s", len(violations), n5xx, strings.Join(violations, "\n"))
	}
}

// The parser and the checks are themselves tested on a fixture with each bad
// shape, so a green run means the rule held, not that the test saw nothing.
func TestPAC21ContractCheckCatchesBadShapes(t *testing.T) {
	fixture := `openapi: 3.1.0
paths:
  /a:
    get:
      parameters:
      - name: q
        in: query
        description: 'a: b'
      responses:
        '500':
          description: The raw Go error
            continues here.
          content:
            text/plain:
              schema:
                type: string
        '502':
          $ref: '#/components/responses/Good'
        '503':
          description: fine
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Wide'
        '504':
          description: >-
            Body is err.Error()
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Envelope'
components:
  responses:
    Good:
      description: Body is {"error":"internal error"}.
      content:
        application/json:
          schema:
            $ref: '#/components/schemas/Envelope'
  schemas:
    Envelope:
      type: object
      required:
      - error
      properties:
        error:
          type: string
    Wide:
      type: object
      properties:
        error:
          type: string
        detail:
          type: string
`
	path := filepath.Join(t.TempDir(), "bundle.yaml")
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	root := parseBundle(t, path)
	responses := root.get("paths", "/a", "get", "responses")
	var got []string
	for _, code := range responses.order {
		got = append(got, check5xxResponse(t, root, code, responses.get(code))...)
	}
	sort.Strings(got)
	want := []string{
		"500: description says the body carries a raw error: raw Go err",
		"500: documents text/plain, not application/json",
		"503: schema Wide is not an {error: string} envelope",
		"504: description says the body carries a raw error: err.Error",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

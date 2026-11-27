package kinddiff

import (
	"fmt"
	"io/fs"
	"strconv"
	"strings"
)

type openapiFile struct {
	root *jsonValue
	ops  []*openapiOperation
}

type openapiOperation struct {
	id     string
	method string
	path   string
	node   *jsonValue
	shape  *jsonValue
	order  int
}

func readOpenAPI(fsys fs.FS, side string) (*openapiFile, error) {
	b, err := fs.ReadFile(fsys, "openapi.json")
	if err != nil {
		return nil, fmt.Errorf("kinddiff: the %s requests directory has no openapi.json: %w", side, err)
	}
	root, err := parseOrderedJSON(b)
	if err != nil {
		return nil, fmt.Errorf("kinddiff: the %s openapi.json does not read: %w", side, err)
	}
	if root.kind != jsonObject {
		return nil, fmt.Errorf("kinddiff: the %s openapi.json is not an object", side)
	}
	out := &openapiFile{root: root}
	paths, _ := root.get("paths")
	if paths == nil || paths.kind != jsonObject {
		return nil, fmt.Errorf("kinddiff: the %s openapi.json has no paths object", side)
	}
	order := 0
	for _, p := range paths.obj {
		if p.val.kind != jsonObject {
			continue
		}
		for _, m := range p.val.obj {
			op := m.val
			if op.kind != jsonObject {
				continue
			}
			idv, _ := op.get("operationId")
			if idv == nil || idv.kind != jsonString || idv.str == "" {
				continue
			}
			one := &openapiOperation{id: idv.str, method: strings.ToUpper(m.key), path: p.key, node: op, order: order}
			order++
			shape, err := canonicalOperation(root, one)
			if err != nil {
				return nil, fmt.Errorf("kinddiff: the %s openapi.json operation %s does not read: %w", side, one.id, err)
			}
			one.shape = shape
			out.ops = append(out.ops, one)
		}
	}
	return out, nil
}

func operationOfKind(op *openapiOperation, kind string) bool {
	if x, ok := op.node.get("x-ritsu-request"); ok {
		if x.kind == jsonObject {
			if n, ok := x.get("name"); ok && n.kind == jsonString {
				return n.str == kind
			}
		}
		return false
	}
	prefix := "/requests/" + kind + "/"
	if strings.HasPrefix(op.path, prefix) {
		return true
	}
	if tags, ok := op.node.get("tags"); ok && tags.kind == jsonArray {
		for _, tag := range tags.arr {
			if tag.kind == jsonString && tag.str == kind {
				return true
			}
		}
	}
	return false
}

// jsonPointer answers the value a local reference points to.
func jsonPointer(root *jsonValue, ref string) (*jsonValue, bool) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, false
	}
	v := root
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		switch v.kind {
		case jsonObject:
			x, ok := v.get(part)
			if !ok {
				return nil, false
			}
			v = x
		case jsonArray:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(v.arr) {
				return nil, false
			}
			v = v.arr[i]
		default:
			return nil, false
		}
	}
	return v, true
}

// resolveRefs expands local references. A circular reference is kept as its `$ref` marker, which
// is enough for equality and keeps the expansion finite.
func resolveRefs(root, v *jsonValue, stack map[string]bool) (*jsonValue, error) {
	if v == nil {
		return nil, nil
	}
	switch v.kind {
	case jsonArray:
		out := make([]*jsonValue, len(v.arr))
		for i, x := range v.arr {
			r, err := resolveRefs(root, x, stack)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return arrayJSON(out...), nil
	case jsonObject:
		rv, hasRef := v.get("$ref")
		if hasRef && rv.kind == jsonString && strings.HasPrefix(rv.str, "#/") {
			if stack[rv.str] {
				return objectJSON(pair("$ref", stringJSON(rv.str))), nil
			}
			target, ok := jsonPointer(root, rv.str)
			if !ok {
				return nil, fmt.Errorf("the reference %s has no target", rv.str)
			}
			stack[rv.str] = true
			resolved, err := resolveRefs(root, target, stack)
			delete(stack, rv.str)
			if err != nil {
				return nil, err
			}
			if resolved.kind != jsonObject {
				return resolved, nil
			}
			pairs := append([]jsonPair(nil), resolved.obj...)
			for _, p := range v.obj {
				if p.key == "$ref" {
					continue
				}
				r, err := resolveRefs(root, p.val, stack)
				if err != nil {
					return nil, err
				}
				found := false
				for i := range pairs {
					if pairs[i].key == p.key {
						pairs[i].val = r
						found = true
						break
					}
				}
				if !found {
					pairs = append(pairs, pair(p.key, r))
				}
			}
			return objectJSON(pairs...), nil
		}
		pairs := make([]jsonPair, 0, len(v.obj))
		for _, p := range v.obj {
			r, err := resolveRefs(root, p.val, stack)
			if err != nil {
				return nil, err
			}
			pairs = append(pairs, pair(p.key, r))
		}
		return objectJSON(pairs...), nil
	default:
		return cloneJSON(v), nil
	}
}

func resolveAll(root, v *jsonValue) (*jsonValue, error) {
	return resolveRefs(root, v, map[string]bool{})
}

// canonicalOperation keeps only what a caller can send or receive: where the operation is, its
// parameters, what its request body is, and what its answers are. Summaries, descriptions and the
// request's version marker are not part of that shape.
func canonicalOperation(root *jsonValue, op *openapiOperation) (*jsonValue, error) {
	resolved, err := resolveAll(root, op.node)
	if err != nil {
		return nil, err
	}
	params := objectJSON()
	if pv, ok := resolved.get("parameters"); ok && pv.kind == jsonArray {
		pairs := make([]jsonPair, 0, len(pv.arr))
		for _, x := range pv.arr {
			if x.kind != jsonObject {
				continue
			}
			name, _ := x.get("name")
			in, _ := x.get("in")
			key := in.strOr("") + ":" + name.strOr("")
			pairs = append(pairs, pair(key, canonicalParameter(x)))
		}
		params = objectJSON(pairs...)
	}
	var body *jsonValue = nullJSON()
	if rb, ok := resolved.get("requestBody"); ok {
		body = canonicalRequestBody(rb)
	}
	responses := objectJSON()
	if rs, ok := resolved.get("responses"); ok && rs.kind == jsonObject {
		pairs := make([]jsonPair, 0, len(rs.obj))
		for _, r := range rs.obj {
			pairs = append(pairs, pair(r.key, canonicalResponse(r.val)))
		}
		responses = objectJSON(pairs...)
	}
	return objectJSON(
		pair("method", stringJSON(op.method)),
		pair("path", stringJSON(op.path)),
		pair("parameters", params),
		pair("requestBody", body),
		pair("responses", responses),
	), nil
}

func keepKeys(v *jsonValue, keys ...string) *jsonValue {
	if v == nil || v.kind != jsonObject {
		return cloneJSON(v)
	}
	var pairs []jsonPair
	for _, key := range keys {
		if x, ok := v.get(key); ok {
			pairs = append(pairs, pair(key, x))
		}
	}
	return objectJSON(pairs...)
}

func canonicalParameter(v *jsonValue) *jsonValue {
	kept := keepKeys(v, "required", "style", "explode", "allowEmptyValue", "allowReserved")
	var pairs []jsonPair
	if kept.kind == jsonObject {
		pairs = append(pairs, kept.obj...)
	}
	if s, ok := v.get("schema"); ok {
		pairs = append(pairs, pair("schema", cleanSchema(s)))
	}
	if c, ok := v.get("content"); ok {
		pairs = append(pairs, pair("content", canonicalContent(c)))
	}
	return objectJSON(pairs...)
}

func canonicalRequestBody(v *jsonValue) *jsonValue {
	if v == nil || v.kind != jsonObject {
		return cloneJSON(v)
	}
	var pairs []jsonPair
	if r, ok := v.get("required"); ok {
		pairs = append(pairs, pair("required", cloneJSON(r)))
	}
	if c, ok := v.get("content"); ok {
		pairs = append(pairs, pair("content", canonicalContent(c)))
	}
	return objectJSON(pairs...)
}

func canonicalResponse(v *jsonValue) *jsonValue {
	if v == nil || v.kind != jsonObject {
		return cloneJSON(v)
	}
	var pairs []jsonPair
	if h, ok := v.get("headers"); ok && h.kind == jsonObject {
		hp := make([]jsonPair, 0, len(h.obj))
		for _, x := range h.obj {
			hp = append(hp, pair(strings.ToLower(x.key), canonicalParameter(x.val)))
		}
		pairs = append(pairs, pair("headers", objectJSON(hp...)))
	}
	if c, ok := v.get("content"); ok {
		pairs = append(pairs, pair("content", canonicalContent(c)))
	}
	return objectJSON(pairs...)
}

func canonicalContent(v *jsonValue) *jsonValue {
	if v == nil || v.kind != jsonObject {
		return cloneJSON(v)
	}
	pairs := make([]jsonPair, 0, len(v.obj))
	for _, media := range v.obj {
		var mp []jsonPair
		if media.val.kind == jsonObject {
			if s, ok := media.val.get("schema"); ok {
				mp = append(mp, pair("schema", cleanSchema(s)))
			}
			if e, ok := media.val.get("encoding"); ok {
				mp = append(mp, pair("encoding", cloneJSON(e)))
			}
		}
		pairs = append(pairs, pair(strings.ToLower(media.key), objectJSON(mp...)))
	}
	return objectJSON(pairs...)
}

// cleanSchema drops annotations that do not change what JSON is valid. A changed explanation is a
// change of the document, but not a change of the API's shape.
func cleanSchema(v *jsonValue) *jsonValue {
	if v == nil {
		return nil
	}
	switch v.kind {
	case jsonArray:
		out := make([]*jsonValue, len(v.arr))
		for i, x := range v.arr {
			out[i] = cleanSchema(x)
		}
		return arrayJSON(out...)
	case jsonObject:
		pairs := make([]jsonPair, 0, len(v.obj))
		for _, p := range v.obj {
			switch {
			case p.key == "description" || p.key == "title" || p.key == "example" || p.key == "examples" || p.key == "deprecated" || p.key == "readOnly" || p.key == "writeOnly" || p.key == "$comment":
				continue
			case strings.HasPrefix(p.key, "x-"):
				continue
			}
			pairs = append(pairs, pair(p.key, cleanSchema(p.val)))
		}
		return objectJSON(pairs...)
	default:
		return cloneJSON(v)
	}
}

func compareOpenAPIs(old, new *openapiFile, kind string) []change {
	var changes []change
	var oldOps, newOps []*openapiOperation
	if old != nil {
		for _, op := range old.ops {
			if operationOfKind(op, kind) {
				oldOps = append(oldOps, op)
			}
		}
	}
	if new != nil {
		for _, op := range new.ops {
			if operationOfKind(op, kind) {
				newOps = append(newOps, op)
			}
		}
	}
	oldOrder, oldItems := operationOrder(oldOps)
	newOrder, newItems := operationOrder(newOps)
	for _, id := range unionOrder(oldOrder, newOrder) {
		ov, inOld := oldItems[id]
		nv, inNew := newItems[id]
		switch {
		case inOld && !inNew:
			addChange(&changes, "api",
				sprintf("The operation `%s` (`%s %s`) is removed.", id, ov.method, ov.path),
				sprintf("操作 `%s`（`%s %s`）がなくなります。", id, ov.method, ov.path),
				true)
		case !inOld && inNew:
			addChange(&changes, "api",
				sprintf("The operation `%s` (`%s %s`) is added.", id, nv.method, nv.path),
				sprintf("操作 `%s`（`%s %s`）が追加されます。", id, nv.method, nv.path),
				true)
		default:
			compareOperationShape(nv, ov.shape, nv.shape, &changes)
		}
	}
	return changes
}

func operationOrder(ops []*openapiOperation) ([]string, map[string]*openapiOperation) {
	order := make([]string, len(ops))
	items := map[string]*openapiOperation{}
	for i, op := range ops {
		id := op.id
		if _, ok := items[id]; ok {
			id += "#" + strconv.Itoa(i+1)
		}
		order[i] = id
		items[id] = op
	}
	return order, items
}

type shapeDifference struct {
	how    string
	path   []string
	before *jsonValue
	after  *jsonValue
}

func compareOperationShape(op *openapiOperation, old, new *jsonValue, changes *[]change) {
	var diffs []shapeDifference
	diffShape(old, new, nil, &diffs)
	for _, d := range diffs {
		if len(d.path) == 1 && d.path[0] == "method" {
			addChange(changes, "api",
				sprintf("The method of the operation `%s` changes from `%s` to `%s`.", op.id, d.before.strOr(""), d.after.strOr("")),
				sprintf("操作 `%s`のメソッドが`%s`から`%s`に変わります。", op.id, d.before.strOr(""), d.after.strOr("")),
				true)
			continue
		}
		if len(d.path) == 1 && d.path[0] == "path" {
			addChange(changes, "api",
				sprintf("The path of the operation `%s` changes from `%s` to `%s`.", op.id, d.before.strOr(""), d.after.strOr("")),
				sprintf("操作 `%s`のパスが`%s`から`%s`に変わります。", op.id, d.before.strOr(""), d.after.strOr("")),
				true)
			continue
		}
		describeAPIShape(op, d, changes)
	}
}

func scalarJSON(v *jsonValue) bool {
	return v == nil || v.kind == jsonNull || v.kind == jsonBool || v.kind == jsonString || v.kind == jsonNumber
}

func diffShape(old, new *jsonValue, path []string, out *[]shapeDifference) {
	if sameJSON(old, new) {
		return
	}
	if old != nil && new != nil && old.kind == jsonObject && new.kind == jsonObject {
		for _, key := range keyUnion(old, new) {
			ov, inOld := old.get(key)
			nv, inNew := new.get(key)
			p := append(append([]string(nil), path...), key)
			switch {
			case inOld && !inNew:
				*out = append(*out, shapeDifference{how: "removed", path: p, before: ov})
			case !inOld && inNew:
				*out = append(*out, shapeDifference{how: "added", path: p, after: nv})
			default:
				diffShape(ov, nv, p, out)
			}
		}
		return
	}
	if old != nil && new != nil && old.kind == jsonArray && new.kind == jsonArray {
		scalar := true
		for _, x := range old.arr {
			scalar = scalar && scalarJSON(x)
		}
		for _, x := range new.arr {
			scalar = scalar && scalarJSON(x)
		}
		if scalar {
			for _, x := range old.arr {
				found := false
				for _, y := range new.arr {
					if sameJSON(x, y) {
						found = true
						break
					}
				}
				if !found {
					*out = append(*out, shapeDifference{how: "value_removed", path: append([]string(nil), path...), before: x})
				}
			}
			for _, x := range new.arr {
				found := false
				for _, y := range old.arr {
					if sameJSON(x, y) {
						found = true
						break
					}
				}
				if !found {
					*out = append(*out, shapeDifference{how: "value_added", path: append([]string(nil), path...), after: x})
				}
			}
			return
		}
	}
	*out = append(*out, shapeDifference{how: "changed", path: append([]string(nil), path...), before: old, after: new})
}

func describeAPIShape(op *openapiOperation, d shapeDifference, changes *[]change) {
	if len(d.path) == 0 {
		return
	}
	switch d.path[0] {
	case "parameters":
		describeParameterShape(op, d, changes)
	case "requestBody":
		describeRequestBodyShape(op, d, changes)
	case "responses":
		describeResponseShape(op, d, changes)
	default:
		addChange(changes, "api",
			sprintf("The operation `%s` changes at `%s` from %s to %s.", op.id, strings.Join(d.path, "."), valueWord("en", d.before), valueWord("en", d.after)),
			sprintf("操作 `%s`の`%s`が%sから%sに変わります。", op.id, strings.Join(d.path, "."), valueWord("ja", d.before), valueWord("ja", d.after)),
			true)
	}
}

func parameterName(key string) (string, string) {
	in, name, _ := strings.Cut(key, ":")
	return in, name
}

func describeParameterShape(op *openapiOperation, d shapeDifference, changes *[]change) {
	if len(d.path) < 2 {
		apiGenericValue(op, d, "the parameters", "パラメーター", changes)
		return
	}
	in, name := parameterName(d.path[1])
	enParam := sprintf("`%s` in %s", name, in)
	jaParam := sprintf("%sの`%s`", in, name)
	if len(d.path) == 2 {
		switch d.how {
		case "added":
			addChange(changes, "api",
				sprintf("The operation `%s` adds the parameter %s.", op.id, enParam),
				sprintf("操作 `%s`にパラメーター%sが追加されます。", op.id, jaParam),
				true)
		case "removed":
			addChange(changes, "api",
				sprintf("The operation `%s` removes the parameter %s.", op.id, enParam),
				sprintf("操作 `%s`のパラメーター%sがなくなります。", op.id, jaParam),
				true)
		default:
			apiGenericValue(op, d, "the parameter "+enParam, sprintf("パラメーター%s", jaParam), changes)
		}
		return
	}
	whereEn := "the parameter " + enParam + " of `" + op.id + "`"
	whereJa := sprintf("操作 `%s`のパラメーター%s", op.id, jaParam)
	describeSchemaDifference(d, whereEn, whereJa, changes)
}

func describeRequestBodyShape(op *openapiOperation, d shapeDifference, changes *[]change) {
	if len(d.path) == 1 {
		switch d.how {
		case "changed":
			if scalarJSON(d.before) && d.before.kind == jsonNull {
				addChange(changes, "api",
					sprintf("The operation `%s` gains a request body.", op.id),
					sprintf("操作 `%s` に送る本文が加わります。", op.id),
					true)
			} else if scalarJSON(d.after) && d.after.kind == jsonNull {
				addChange(changes, "api",
					sprintf("The operation `%s` loses its request body.", op.id),
					sprintf("操作 `%s` に送る本文がなくなります。", op.id),
					true)
			} else {
				apiGenericValue(op, d, "the request body", "送る本文", changes)
			}
		default:
			apiGenericValue(op, d, "the request body", "送る本文", changes)
		}
		return
	}
	if len(d.path) >= 3 && d.path[1] == "content" && len(d.path) == 3 {
		media := d.path[2]
		if d.how == "added" {
			addChange(changes, "api",
				sprintf("The request body of `%s` adds the media type `%s`.", op.id, media),
				sprintf("`%s` に送る本文にメディアタイプ `%s` が追加されます。", op.id, media),
				true)
		} else if d.how == "removed" {
			addChange(changes, "api",
				sprintf("The request body of `%s` removes the media type `%s`.", op.id, media),
				sprintf("`%s` に送る本文のメディアタイプ `%s` がなくなります。", op.id, media),
				true)
		} else {
			apiGenericValue(op, d, "the request body's media type `"+media+"`", sprintf("本文のメディアタイプ `%s`", media), changes)
		}
		return
	}
	whereEn := "the request body of `" + op.id + "`"
	whereJa := sprintf("`%s`に送る本文", op.id)
	describeSchemaDifference(d, whereEn, whereJa, changes)
}

func describeResponseShape(op *openapiOperation, d shapeDifference, changes *[]change) {
	if len(d.path) < 2 {
		apiGenericValue(op, d, "the responses", "応答", changes)
		return
	}
	status := d.path[1]
	if len(d.path) == 2 {
		if d.how == "added" {
			addChange(changes, "api",
				sprintf("The operation `%s` adds the response `%s`.", op.id, status),
				sprintf("操作 `%s`に応答`%s`が追加されます。", op.id, status),
				true)
		} else if d.how == "removed" {
			addChange(changes, "api",
				sprintf("The operation `%s` removes the response `%s`.", op.id, status),
				sprintf("操作 `%s`の応答`%s`がなくなります。", op.id, status),
				true)
		} else {
			apiGenericValue(op, d, "the response `"+status+"`", sprintf("応答 `%s`", status), changes)
		}
		return
	}
	if len(d.path) >= 4 && d.path[2] == "content" && len(d.path) == 4 {
		media := d.path[3]
		if d.how == "added" {
			addChange(changes, "api",
				sprintf("The `%s` response of `%s` adds the media type `%s`.", status, op.id, media),
				sprintf("`%s`の応答`%s`にメディアタイプ`%s`が追加されます。", op.id, status, media),
				true)
		} else if d.how == "removed" {
			addChange(changes, "api",
				sprintf("The `%s` response of `%s` removes the media type `%s`.", status, op.id, media),
				sprintf("`%s`の応答`%s`のメディアタイプ`%s`がなくなります。", op.id, status, media),
				true)
		} else {
			apiGenericValue(op, d, "the response's media type `"+media+"`", sprintf("応答のメディアタイプ `%s`", media), changes)
		}
		return
	}
	if len(d.path) >= 3 && d.path[2] == "headers" {
		header := ""
		if len(d.path) > 3 {
			header = d.path[3]
		}
		if len(d.path) == 4 && d.how == "added" {
			addChange(changes, "api",
				sprintf("The `%s` response of `%s` adds the header `%s`.", status, op.id, header),
				sprintf("`%s`の応答`%s`にヘッダー`%s`が追加されます。", op.id, status, header),
				true)
			return
		}
		if len(d.path) == 4 && d.how == "removed" {
			addChange(changes, "api",
				sprintf("The `%s` response of `%s` removes the header `%s`.", status, op.id, header),
				sprintf("`%s`の応答`%s`のヘッダー`%s`がなくなります。", op.id, status, header),
				true)
			return
		}
	}
	whereEn := "the `" + status + "` response of `" + op.id + "`"
	whereJa := sprintf("`%s`の応答 `%s`", op.id, status)
	describeSchemaDifference(d, whereEn, whereJa, changes)
}

func apiGenericValue(op *openapiOperation, d shapeDifference, enWhat, jaWhat string, changes *[]change) {
	verbEn, verbJa := "changes", "変わります"
	if d.how == "added" || d.how == "value_added" {
		verbEn, verbJa = "is added", "追加されます"
	} else if d.how == "removed" || d.how == "value_removed" {
		verbEn, verbJa = "is removed", "なくなります"
	}
	if d.how == "changed" {
		addChange(changes, "api",
			sprintf("In %s of `%s`, `%s` changes from %s to %s.", enWhat, op.id, strings.Join(d.path, "."), valueWord("en", d.before), valueWord("en", d.after)),
			sprintf("`%s`の%sでは、`%s`が%sから%sに変わります。", op.id, jaWhat, strings.Join(d.path, "."), valueWord("ja", d.before), valueWord("ja", d.after)),
			true)
		return
	}
	addChange(changes, "api",
		sprintf("In %s of `%s`, `%s` %s: %s.", enWhat, op.id, strings.Join(d.path, "."), verbEn, valueWord("en", firstValue(d))),
		sprintf("`%s`の%sでは、`%s`が%s。%sです。", op.id, jaWhat, strings.Join(d.path, "."), verbJa, valueWord("ja", firstValue(d))),
		true)
}

func firstValue(d shapeDifference) *jsonValue {
	if d.after != nil {
		return d.after
	}
	return d.before
}

// schemaParts reads the path inside a JSON schema. `properties` does not become part of the field
// name; `items` is written as `[]`; what remains is the constraint being changed.
func schemaParts(path []string) (string, string) {
	var fields []string
	var constraints []string
	for i := 0; i < len(path); {
		switch path[i] {
		case "properties":
			if i+1 < len(path) {
				fields = append(fields, path[i+1])
				i += 2
			} else {
				i++
			}
		case "items":
			if len(fields) == 0 {
				fields = append(fields, "[]")
			} else {
				fields[len(fields)-1] += "[]"
			}
			i++
		default:
			constraints = append(constraints, path[i:]...)
			i = len(path)
		}
	}
	return strings.Join(fields, "."), strings.Join(constraints, ".")
}

func schemaTail(path []string) []string {
	for i, part := range path {
		if part == "schema" && i+1 < len(path) {
			return path[i+1:]
		}
	}
	return nil
}

func describeSchemaDifference(d shapeDifference, whereEn, whereJa string, changes *[]change) {
	tail := schemaTail(d.path)
	if len(tail) == 0 {
		apiShapeFallback(d, whereEn, whereJa, changes)
		return
	}
	field, constraint := schemaParts(tail)
	shownField := field
	if shownField == "" {
		shownField = "the value"
	}
	jaField := field
	if jaField == "" {
		jaField = "値"
	}
	if (d.how == "added" || d.how == "removed") && constraint == "" && field != "" {
		if d.how == "added" {
			addChange(changes, "api",
				sprintf("In %s, `%s` is added.", whereEn, field),
				sprintf("%sに`%s`が追加されます。", whereJa, jaField),
				true)
		} else {
			addChange(changes, "api",
				sprintf("In %s, `%s` is removed.", whereEn, field),
				sprintf("%sの`%s`がなくなります。", whereJa, jaField),
				true)
		}
		return
	}
	if constraint == "required" && (d.how == "value_added" || d.how == "value_removed") {
		name := firstValue(d).strOr(firstValue(d).compact())
		if d.how == "value_added" {
			addChange(changes, "api",
				sprintf("In %s, `%s` becomes required.", whereEn, name),
				sprintf("%sでは、`%s`が必須になります。", whereJa, name),
				true)
		} else {
			addChange(changes, "api",
				sprintf("In %s, `%s` is no longer required.", whereEn, name),
				sprintf("%sでは、`%s`は必須ではなくなります。", whereJa, name),
				true)
		}
		return
	}
	if constraint == "enum" && (d.how == "value_added" || d.how == "value_removed") {
		if d.how == "value_added" {
			addChange(changes, "api",
				sprintf("In %s, %s is added to the values `%s` takes.", whereEn, valueWord("en", firstValue(d)), shownField),
				sprintf("%sでは、`%s`の値に%sが追加されます。", whereJa, jaField, valueWord("ja", firstValue(d))),
				true)
		} else {
			addChange(changes, "api",
				sprintf("In %s, %s is removed from the values `%s` takes.", whereEn, valueWord("en", firstValue(d)), shownField),
				sprintf("%sでは、`%s`の値から%sがなくなります。", whereJa, jaField, valueWord("ja", firstValue(d))),
				true)
		}
		return
	}
	if constraint == "type" || (constraint == "" && d.how == "changed" && field != "") {
		what := "the shape"
		jaWhat := "形"
		if constraint == "type" {
			what = "the type"
			jaWhat = "型"
		}
		addChange(changes, "api",
			sprintf("In %s, %s of `%s` changes from %s to %s.", whereEn, what, shownField, valueWord("en", d.before), valueWord("en", d.after)),
			sprintf("%sでは、`%s`の%sが%sから%sに変わります。", whereJa, jaField, jaWhat, valueWord("ja", d.before), valueWord("ja", d.after)),
			true)
		return
	}
	if constraint != "" {
		addChange(changes, "api",
			sprintf("In %s, `%s` of `%s` changes from %s to %s.", whereEn, constraint, shownField, valueWord("en", d.before), valueWord("en", d.after)),
			sprintf("%sでは、`%s`の`%s`が%sから%sに変わります。", whereJa, jaField, constraint, valueWord("ja", d.before), valueWord("ja", d.after)),
			true)
		return
	}
	apiShapeFallback(d, whereEn, whereJa, changes)
}

func apiShapeFallback(d shapeDifference, whereEn, whereJa string, changes *[]change) {
	if d.how == "added" || d.how == "value_added" {
		addChange(changes, "api",
			sprintf("In %s, `%s` is added: %s.", whereEn, strings.Join(d.path, "."), valueWord("en", firstValue(d))),
			sprintf("%sに`%s`が追加されます。%sです。", whereJa, strings.Join(d.path, "."), valueWord("ja", firstValue(d))),
			true)
		return
	}
	if d.how == "removed" || d.how == "value_removed" {
		addChange(changes, "api",
			sprintf("In %s, `%s` is removed: it was %s.", whereEn, strings.Join(d.path, "."), valueWord("en", firstValue(d))),
			sprintf("%sの`%s`がなくなります。これまでは%sでした。", whereJa, strings.Join(d.path, "."), valueWord("ja", firstValue(d))),
			true)
		return
	}
	addChange(changes, "api",
		sprintf("In %s, `%s` changes from %s to %s.", whereEn, strings.Join(d.path, "."), valueWord("en", d.before), valueWord("en", d.after)),
		sprintf("%sでは、`%s`が%sから%sに変わります。", whereJa, strings.Join(d.path, "."), valueWord("ja", d.before), valueWord("ja", d.after)),
		true)
}

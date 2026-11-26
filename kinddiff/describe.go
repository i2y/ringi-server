package kinddiff

import (
	"slices"
	"strconv"
	"strings"
)

// compareDescriptions compares every key of two descriptions. The detailed comparisons are of the
// parts an administrator approves by them: the form, the stages, how approvers are concerned, and
// the days the kind can be used. Every other key is still compared and reported in "other".
func compareDescriptions(old, new *descriptionFile) []change {
	var changes []change
	if old == nil {
		compareNewDescription(new, &changes)
		return changes
	}
	oc, nc := *old, *new
	oc.root, nc.root = compareIntroducedMetadata(old, new, &changes)
	compareDescriptionKeys(&oc, &nc, &changes)
	return changes
}

// compareNewDescription treats everything of a new kind as added. It is verbose on purpose: the
// approval of a new kind is the approval of the whole shape it adds.
func compareNewDescription(new *descriptionFile, changes *[]change) {
	empty := &descriptionFile{root: objectJSON(), language: new.language}
	compareDescriptionKeys(empty, new, changes)
	addChange(changes, "other",
		sprintf("The kind is new, version %d.", new.version),
		sprintf("この申請の種類は新しいものです。版は%dです。", new.version),
		false)
}

// compareIntroducedMetadata treats the approver and deadline metadata written by newer ritsu as
// unavailable, not as added, when the other description does not expose that family at all. The
// behavior existed in the old version too; only the description did not say it. Approver cases are
// listed as current information because they are the part the bundle approval has to show; the keys
// are then left out of the detailed comparison.
func compareIntroducedMetadata(old, new *descriptionFile, changes *[]change) (*jsonValue, *jsonValue) {
	oldRoot, newRoot := old.root, new.root

	oldDeadlines := exposesListKey(oldRoot, "stages", "deadlines")
	newDeadlines := exposesListKey(newRoot, "stages", "deadlines")
	if oldDeadlines != newDeadlines {
		emitDeadlineIntroduction(newDeadlines, changes)
		oldRoot = withoutListKey(oldRoot, "stages", "deadlines")
		newRoot = withoutListKey(newRoot, "stages", "deadlines")
	}

	oldTables := exposesRootKey(oldRoot, "late_tables")
	newTables := exposesRootKey(newRoot, "late_tables")
	if oldTables != newTables {
		emitLateTableIntroduction(newTables, changes)
		oldRoot = withoutRootKey(oldRoot, "late_tables")
		newRoot = withoutRootKey(newRoot, "late_tables")
	}

	oldCases := exposesListKey(oldRoot, "stages", "approvers")
	newCases := exposesListKey(newRoot, "stages", "approvers")
	oldHow := exposesListKey(oldRoot, "ways", "how")
	newHow := exposesListKey(newRoot, "ways", "how")
	if oldCases != newCases || oldHow != newHow {
		newSide := newCases
		if oldCases == newCases {
			newSide = newHow
		}
		emitApproverIntroduction(old, new, newSide, oldCases != newCases, changes)
	}
	if oldCases != newCases {
		oldRoot = withoutListKey(oldRoot, "stages", "approvers")
		newRoot = withoutListKey(newRoot, "stages", "approvers")
	}
	if oldHow != newHow {
		oldRoot = withoutListKey(oldRoot, "ways", "how")
		newRoot = withoutListKey(newRoot, "ways", "how")
	}

	return oldRoot, newRoot
}

func exposesRootKey(root *jsonValue, key string) bool {
	if root == nil {
		return false
	}
	_, ok := root.get(key)
	return ok
}

func exposesListKey(root *jsonValue, listKey, key string) bool {
	list, ok := root.get(listKey)
	if !ok || list.kind != jsonArray {
		return false
	}
	for _, item := range list.arr {
		if _, ok := item.get(key); ok {
			return true
		}
	}
	return false
}

func objectWithoutKey(v *jsonValue, key string) *jsonValue {
	if v == nil || v.kind != jsonObject {
		return v
	}
	if _, ok := v.get(key); !ok {
		return v
	}
	pairs := make([]jsonPair, 0, len(v.obj)-1)
	for _, p := range v.obj {
		if p.key != key {
			pairs = append(pairs, p)
		}
	}
	return objectJSON(pairs...)
}

func objectWithKey(v *jsonValue, key string, val *jsonValue) *jsonValue {
	if v == nil || v.kind != jsonObject {
		return v
	}
	pairs := make([]jsonPair, 0, len(v.obj))
	replaced := false
	for _, p := range v.obj {
		if p.key == key {
			pairs = append(pairs, pair(key, val))
			replaced = true
		} else {
			pairs = append(pairs, p)
		}
	}
	if !replaced {
		pairs = append(pairs, pair(key, val))
	}
	return objectJSON(pairs...)
}

func withoutRootKey(root *jsonValue, key string) *jsonValue {
	return objectWithoutKey(root, key)
}

func withoutListKey(root *jsonValue, listKey, key string) *jsonValue {
	list, ok := root.get(listKey)
	if !ok || list.kind != jsonArray {
		return root
	}
	changed := false
	items := make([]*jsonValue, len(list.arr))
	for i, item := range list.arr {
		items[i] = objectWithoutKey(item, key)
		changed = changed || items[i] != item
	}
	if !changed {
		return root
	}
	return objectWithKey(root, listKey, arrayJSON(items...))
}

func listItems(root *jsonValue, key string) []*jsonValue {
	v, ok := root.get(key)
	if !ok || v.kind != jsonArray {
		return nil
	}
	return v.arr
}

func metadataSide(newSide bool) (enSide, enVerb string) {
	if newSide {
		return "new", "says"
	}
	return "old", "said"
}

func emitApproverIntroduction(old, new *descriptionFile, newSide bool, listCases bool, changes *[]change) {
	if newSide {
		addChange(changes, "approvers",
			"The old version's description does not say how its approvers are chosen, so the change cannot be shown.",
			"古い版の説明には承認者の決め方が書かれていないので、変わったかは分かりません。",
			false)
	} else {
		addChange(changes, "approvers",
			"The new version's description does not say how its approvers are chosen, so the change cannot be shown.",
			"新しい版の説明には承認者の決め方が書かれていないので、変わったかは分かりません。",
			false)
	}
	if !listCases {
		return
	}
	source, sourceDesc := new.root, new
	if !newSide {
		source, sourceDesc = old.root, old
	}
	enSide, enVerb := metadataSide(newSide)
	for _, stage := range listItems(source, "stages") {
		cases, ok := stage.get("approvers")
		if !ok || cases.kind != jsonArray {
			continue
		}
		name := asciiName(stage)
		enStage := displayNamed(stage, "en", sourceDesc.language, name)
		jaStage := displayNamed(stage, "ja", sourceDesc.language, name)
		for _, one := range cases.arr {
			addChange(changes, "approvers",
				sprintf("The %s description %s of the stage %s: %s.", enSide, enVerb, enStage, currentCaseWords(one, "en")),
				sprintf("%sの説明では、ステップ%sの場合として%sと書かれています。", sideJa(newSide), jaStage, currentCaseWords(one, "ja")),
				false)
		}
	}
}

func sideJa(newSide bool) string {
	if newSide {
		return "新しい版"
	}
	return "古い版"
}

func emitDeadlineIntroduction(newSide bool, changes *[]change) {
	if newSide {
		addChange(changes, "stages",
			"The old version's description does not say the deadlines of its stages, so the change cannot be shown.",
			"古い版の説明にはステップの期限と催促が書かれていないので、変わったかは分かりません。",
			false)
	} else {
		addChange(changes, "stages",
			"The new version's description does not say the deadlines of its stages, so the change cannot be shown.",
			"新しい版の説明にはステップの期限と催促が書かれていないので、変わったかは分かりません。",
			false)
	}
}

func emitLateTableIntroduction(newSide bool, changes *[]change) {
	if newSide {
		addChange(changes, "stages",
			"The old version's description does not say the tables by which a late stage goes up, so the change cannot be shown.",
			"古い版の説明には、期限を過ぎたステップが上へ回る表が書かれていないので、変わったかは分かりません。",
			false)
	} else {
		addChange(changes, "stages",
			"The new version's description does not say the tables by which a late stage goes up, so the change cannot be shown.",
			"新しい版の説明には、期限を過ぎたステップが上へ回る表が書かれていないので、変わったかは分かりません。",
			false)
	}
}

func compareDescriptionKeys(old, new *descriptionFile, changes *[]change) {
	for _, key := range keyUnion(old.root, new.root) {
		ov, _ := old.root.get(key)
		nv, _ := new.root.get(key)
		if sameJSON(ov, nv) {
			continue
		}
		switch key {
		case "form":
			compareForm(ov, nv, old, new, changes)
		case "stages":
			compareStages(ov, nv, old, new, changes)
		case "ways":
			compareWays(ov, nv, old, new, changes)
		case "late_tables":
			compareLateTables(ov, nv, changes)
		case "self_approval":
			compareSelfApproval(ov, nv, changes)
		case "approvers_edit":
			compareApproversEdit(ov, nv, old, new, changes)
		case "effective_from":
			compareEffectiveFrom(ov, nv, changes)
		case "usable":
			compareUsable(ov, nv, changes)
		case "utc_offset_minutes":
			compareUTCOffset(ov, nv, changes)
		case "version":
			compareVersion(ov, nv, changes)
		case "endings", "events":
			compareStringSet(key, ov, nv, changes)
		case "submitted_by":
			compareSubmittedBy(ov, nv, changes)
		case "organization":
			compareOrganization(ov, nv, old, new, changes)
		case "masters":
			compareMasters(ov, nv, old, new, changes)
		case "flows":
			compareFlows(ov, nv, changes)
		default:
			compareOtherKey(key, ov, nv, changes)
		}
	}
}

func compareVersion(old, new *jsonValue, changes *[]change) {
	if old == nil {
		addChange(changes, "other",
			"The version is "+new.compact()+".",
			"版は"+new.compact()+"です。",
			false)
		return
	}
	if new == nil {
		addChange(changes, "other",
			"The version is removed.",
			"版の番号がなくなりました。",
			false)
		return
	}
	c := change{
		section: "other",
		en:      sprintf("The version number changes from %s to %s.", old.compact(), new.compact()),
		ja:      sprintf("版の番号が%sから%sに変わります。", old.compact(), new.compact()),
		onlyEn:  sprintf("The only change is the version number, from %s to %s.", old.compact(), new.compact()),
		onlyJa:  sprintf("変更は版の番号だけです。%sから%sに変わります。", old.compact(), new.compact()),
	}
	*changes = append(*changes, c)
}

// formItems answers the array of a list node, or nil for anything else. The callers still report
// a malformed list as one generic change of its parent.
func formItems(v *jsonValue) []*jsonValue {
	if v == nil || v.kind != jsonArray {
		return nil
	}
	return v.arr
}

func fieldName(v *jsonValue, index int) string {
	if v != nil && v.kind == jsonObject {
		if n, ok := v.get("name"); ok && n.kind == jsonString && n.str != "" {
			return n.str
		}
	}
	return "#" + strconv.Itoa(index+1)
}

func keyedOrder(arr []*jsonValue) ([]string, map[string]*jsonValue, map[string]int) {
	order := make([]string, len(arr))
	items := map[string]*jsonValue{}
	positions := map[string]int{}
	for i, item := range arr {
		name := fieldName(item, i)
		if _, ok := items[name]; ok {
			name = name + "#" + strconv.Itoa(i+1)
		}
		order[i] = name
		items[name] = item
		positions[name] = i + 1
	}
	return order, items, positions
}

// movedInSequence answers the names whose relative order changed. Insertions and removals alone do
// not make everything after them moved: the order of the names present on both sides is compared,
// and a longest common subsequence is taken as unmoved.
func movedInSequence(oldOrder, newOrder []string) map[string]bool {
	inNew := map[string]bool{}
	for _, x := range newOrder {
		inNew[x] = true
	}
	var oldCommon []string
	for _, x := range oldOrder {
		if inNew[x] {
			oldCommon = append(oldCommon, x)
		}
	}
	inOld := map[string]bool{}
	for _, x := range oldOrder {
		inOld[x] = true
	}
	var newCommon []string
	for _, x := range newOrder {
		if inOld[x] {
			newCommon = append(newCommon, x)
		}
	}
	keep := lcs(oldCommon, newCommon)
	moved := map[string]bool{}
	for _, x := range oldCommon {
		if !keep[x] {
			moved[x] = true
		}
	}
	return moved
}

func lcs(a, b []string) map[string]bool {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	keep := map[string]bool{}
	for i, j := 0, 0; i < len(a) && j < len(b); {
		if a[i] == b[j] {
			keep[a[i]] = true
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			i++
		} else {
			j++
		}
	}
	return keep
}

func unionOrder(oldOrder, newOrder []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range oldOrder {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	for _, x := range newOrder {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

func childPath(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "." + name
}

func displayField(item *jsonValue, lang, fileLang, path string) string {
	return displayNamed(item, lang, fileLang, path)
}

func compareForm(old, new *jsonValue, od, nd *descriptionFile, changes *[]change) {
	if (old != nil && old.kind != jsonArray) || (new != nil && new.kind != jsonArray) {
		compareOtherKey("form", old, new, changes)
		return
	}
	compareFieldList(formItems(old), formItems(new), "", od, nd, changes)
}

func fieldSummary(item *jsonValue, lang string) string {
	if item == nil || item.kind != jsonObject {
		return ""
	}
	typ, _ := item.get("type")
	optional, _ := item.get("optional")
	min, _ := item.get("min")
	max, _ := item.get("max")
	var en []string
	var ja []string
	if typ != nil && typ.kind == jsonString {
		en = append(en, "type "+valueWord("en", typ))
		ja = append(ja, "型は"+valueWord("ja", typ))
	}
	if optional != nil {
		if optional.boolOr(false) {
			en = append(en, "optional")
			ja = append(ja, "なくてもよい")
		} else {
			en = append(en, "required")
			ja = append(ja, "必須")
		}
	}
	if min != nil || max != nil {
		en = append(en, "range "+rangeText("en", min, max))
		ja = append(ja, "範囲は"+rangeText("ja", min, max))
	}
	if lang == "ja" {
		return strings.Join(ja, "、")
	}
	return strings.Join(en, ", ")
}

func compareFieldList(oldArr, newArr []*jsonValue, parent string, od, nd *descriptionFile, changes *[]change) {
	oldOrder, oldItems, oldPos := keyedOrder(oldArr)
	newOrder, newItems, newPos := keyedOrder(newArr)
	moved := movedInSequence(oldOrder, newOrder)
	for _, name := range unionOrder(oldOrder, newOrder) {
		ov, inOld := oldItems[name]
		nv, inNew := newItems[name]
		path := childPath(parent, name)
		displayOf := nv
		displaySide := nd
		if !inNew {
			displayOf = ov
			displaySide = od
		}
		enName := displayField(displayOf, "en", displaySide.language, path)
		jaName := displayField(displayOf, "ja", displaySide.language, path)
		switch {
		case inOld && !inNew:
			addChange(changes, "form",
				sprintf("The field %s is removed.", enName),
				sprintf("項目%sがなくなりました。", jaName),
				false)
			continue
		case !inOld && inNew:
			enText := sprintf("The field %s is added.", enName)
			jaText := sprintf("項目%sが追加されました。", jaName)
			if summary := fieldSummary(nv, "en"); summary != "" {
				enText = sprintf("The field %s is added: %s.", enName, summary)
			}
			if summary := fieldSummary(nv, "ja"); summary != "" {
				jaText = sprintf("項目%sが追加されました。%sです。", jaName, summary)
			}
			addChange(changes, "form", enText, jaText, false)
			continue
		}
		if moved[name] {
			addChange(changes, "form",
				sprintf("The field %s moves from position %d to position %d.", enName, oldPos[name], newPos[name]),
				sprintf("項目%sの位置が%d番目から%d番目に変わります。", jaName, oldPos[name], newPos[name]),
				false)
		}
		compareField(ov, nv, path, od, nd, changes)
	}
}

var fieldKeys = []string{"written", "label", "labels", "type", "type_written", "min", "max", "count_min", "count_max", "choices", "optional", "computed", "reads", "worked_on_screen", "approvers_only", "open_in", "shown_when", "required_when", "fields"}

func orderedFieldKeys(old, new *jsonValue) []string {
	all := keyUnion(old, new)
	var out []string
	for _, key := range fieldKeys {
		if slices.Contains(all, key) {
			out = append(out, key)
		}
	}
	for _, key := range all {
		if key != "name" && !slices.Contains(out, key) {
			out = append(out, key)
		}
	}
	return out
}

func compareField(old, new *jsonValue, path string, od, nd *descriptionFile, changes *[]change) {
	enName := displayField(new, "en", nd.language, path)
	jaName := displayField(new, "ja", nd.language, path)
	doneRange := false
	doneCount := false
	for _, key := range orderedFieldKeys(old, new) {
		ov, _ := old.get(key)
		nv, _ := new.get(key)
		if sameJSON(ov, nv) {
			continue
		}
		switch key {
		case "min", "max":
			if doneRange {
				continue
			}
			doneRange = true
			omin, _ := old.get("min")
			omax, _ := old.get("max")
			nmin, _ := new.get("min")
			nmax, _ := new.get("max")
			compareRange(enName, jaName, omin, omax, nmin, nmax, changes)
		case "count_min", "count_max":
			if doneCount {
				continue
			}
			doneCount = true
			omin, _ := old.get("count_min")
			omax, _ := old.get("count_max")
			nmin, _ := new.get("count_min")
			nmax, _ := new.get("count_max")
			compareCountRange(enName, jaName, omin, omax, nmin, nmax, changes)
		case "type":
			compareFieldValue(key, enName, jaName, ov, nv, changes,
				"the type", "型")
		case "type_written":
			compareFieldValue(key, enName, jaName, ov, nv, changes,
				"the type as written", "書かれたままの型")
		case "label":
			compareFieldValue(key, enName, jaName, ov, nv, changes,
				"the label", "ラベル")
		case "labels":
			compareFieldValue(key, enName, jaName, ov, nv, changes,
				"the labels in other languages", "ほかの言語のラベル")
		case "written":
			compareFieldValue(key, enName, jaName, ov, nv, changes,
				"the name as written", "書かれたままの名前")
		case "choices":
			compareChoices(ov, nv, path, od, nd, changes)
		case "optional":
			if nv.boolOr(false) {
				addChange(changes, "form",
					sprintf("The field %s becomes optional.", enName),
					sprintf("項目%sは、なくてもよい項目になります。", jaName),
					false)
			} else {
				addChange(changes, "form",
					sprintf("The field %s becomes required.", enName),
					sprintf("項目%sは必須になります。", jaName),
					false)
			}
		case "computed":
			if nv.boolOr(false) {
				addChange(changes, "form",
					sprintf("The field %s becomes computed.", enName),
					sprintf("項目%sは自動で計算されるようになります。", jaName),
					false)
			} else {
				addChange(changes, "form",
					sprintf("The field %s is no longer computed.", enName),
					sprintf("項目%sは自動では計算されなくなります。", jaName),
					false)
			}
		case "reads":
			addChange(changes, "form",
				sprintf("The fields read to work out %s change from %s to %s.", enName, valueWord("en", ov), valueWord("en", nv)),
				sprintf("項目%sを計算するときに読む項目が%sから%sに変わります。", jaName, valueWord("ja", ov), valueWord("ja", nv)),
				false)
		case "worked_on_screen":
			if nv.boolOr(false) {
				addChange(changes, "form",
					sprintf("The field %s is now worked out on the screen.", enName),
					sprintf("項目%sは画面で計算されるようになります。", jaName),
					false)
			} else {
				addChange(changes, "form",
					sprintf("The field %s is no longer worked out on the screen.", enName),
					sprintf("項目%sは画面では自動では計算されなくなります。", jaName),
					false)
			}
		case "approvers_only":
			if nv.boolOr(false) {
				addChange(changes, "form",
					sprintf("The field %s becomes one only approvers fill in.", enName),
					sprintf("項目%sは承認者だけが入力するようになります。", jaName),
					false)
			} else {
				addChange(changes, "form",
					sprintf("The field %s is no longer one only approvers fill in.", enName),
					sprintf("項目%sは承認者だけが入力するものではなくなります。", jaName),
					false)
			}
		case "open_in":
			compareFieldValue(key, enName, jaName, ov, nv, changes,
				"the calendar of open days", "入力できる日を数えるカレンダー")
		case "shown_when":
			compareCondition("shown", enName, jaName, ov, nv, changes)
		case "required_when":
			compareCondition("required", enName, jaName, ov, nv, changes)
		case "fields":
			compareFieldList(formItems(ov), formItems(nv), path, od, nd, changes)
		default:
			compareFieldValue(key, enName, jaName, ov, nv, changes,
				"the "+keyWord(key), keyWord(key))
		}
	}
}

func compareFieldValue(key string, enName, jaName string, old, new *jsonValue, changes *[]change, enWhat, jaWhat string) {
	enWhat = strings.ReplaceAll(enWhat, "`", "")
	if old == nil {
		addChange(changes, "form",
			sprintf("The field %s gains %s: %s.", enName, enWhat, valueWord("en", new)),
			sprintf("項目%sに%sとして%sが加わります。", jaName, jaWhat, valueWord("ja", new)),
			false)
		return
	}
	if new == nil {
		addChange(changes, "form",
			sprintf("The field %s loses %s: it was %s.", enName, enWhat, valueWord("en", old)),
			sprintf("項目%sの%sがなくなります。これまでは%sでした。", jaName, jaWhat, valueWord("ja", old)),
			false)
		return
	}
	addChange(changes, "form",
		sprintf("The %s of the field %s changes from %s to %s.", strings.TrimPrefix(enWhat, "the "), enName, valueWord("en", old), valueWord("en", new)),
		sprintf("項目%sの%sが%sから%sに変わります。", jaName, jaWhat, valueWord("ja", old), valueWord("ja", new)),
		false)
}

func rangeText(lang string, min, max *jsonValue) string {
	hasMin := min != nil && min.kind != jsonNull
	hasMax := max != nil && max.kind != jsonNull
	if lang == "ja" {
		switch {
		case hasMin && hasMax:
			return dayWord(min) + "以上" + dayWord(max) + "以下"
		case hasMin:
			return dayWord(min) + "以上"
		case hasMax:
			return dayWord(max) + "以下"
		}
		return "制限なし"
	}
	switch {
	case hasMin && hasMax:
		return dayWord(min) + " through " + dayWord(max)
	case hasMin:
		return dayWord(min) + " or more"
	case hasMax:
		return dayWord(max) + " or less"
	}
	return "without bounds"
}

func compareRange(enName, jaName string, omin, omax, nmin, nmax *jsonValue, changes *[]change) {
	addChange(changes, "form",
		sprintf("The range of the field %s changes from %s to %s.", enName, rangeText("en", omin, omax), rangeText("en", nmin, nmax)),
		sprintf("項目%sの範囲が%sから%sに変わります。", jaName, rangeText("ja", omin, omax), rangeText("ja", nmin, nmax)),
		false)
}

func compareCountRange(enName, jaName string, omin, omax, nmin, nmax *jsonValue, changes *[]change) {
	addChange(changes, "form",
		sprintf("The limits on the length or rows of the field %s change from %s to %s.", enName, rangeText("en", omin, omax), rangeText("en", nmin, nmax)),
		sprintf("項目%sの文字数や行数の制限が%sから%sに変わります。", jaName, rangeText("ja", omin, omax), rangeText("ja", nmin, nmax)),
		false)
}

func compareChoices(old, new *jsonValue, path string, od, nd *descriptionFile, changes *[]change) {
	if (old != nil && old.kind != jsonArray) || (new != nil && new.kind != jsonArray) {
		enName := displayField(new, "en", nd.language, path)
		jaName := displayField(new, "ja", nd.language, path)
		compareFieldValue("choices", enName, jaName, old, new, changes, "the choices", "選択肢")
		return
	}
	oldArr, newArr := formItems(old), formItems(new)
	oldOrder, oldItems, _ := keyedOrder(oldArr)
	newOrder, newItems, _ := keyedOrder(newArr)
	parentEn := displayField(new, "en", nd.language, path)
	parentJa := displayField(new, "ja", nd.language, path)
	for _, name := range unionOrder(oldOrder, newOrder) {
		ov, inOld := oldItems[name]
		nv, inNew := newItems[name]
		displayOf := nv
		displaySide := nd
		if !inNew {
			displayOf = ov
			displaySide = od
		}
		enChoice := displayNamed(displayOf, "en", displaySide.language, name)
		jaChoice := displayNamed(displayOf, "ja", displaySide.language, name)
		switch {
		case inOld && !inNew:
			addChange(changes, "form",
				sprintf("The choice %s of the field %s is removed.", enChoice, parentEn),
				sprintf("項目%sの選択肢%sがなくなります。", parentJa, jaChoice),
				false)
		case !inOld && inNew:
			addChange(changes, "form",
				sprintf("The choice %s of the field %s is added.", enChoice, parentEn),
				sprintf("項目%sに選択肢%sが追加されます。", parentJa, jaChoice),
				false)
		default:
			if !sameJSON(ov, nv) {
				addChange(changes, "form",
					sprintf("The choice %s of the field %s changes from %s to %s.", enChoice, parentEn, valueWord("en", ov), valueWord("en", nv)),
					sprintf("項目%sの選択肢%sが%sから%sに変わります。", parentJa, jaChoice, valueWord("ja", ov), valueWord("ja", nv)),
					false)
			}
		}
	}
	if slices.Equal(oldOrder, newOrder) || !sameStringSet(oldOrder, newOrder) {
		return
	}
	addChange(changes, "form",
		sprintf("The order of the choices of the field %s changes.", parentEn),
		sprintf("項目%sの選択肢の順が変わります。", parentJa),
		false)
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, x := range a {
		seen[x]++
	}
	for _, x := range b {
		seen[x]--
		if seen[x] < 0 {
			return false
		}
	}
	return true
}

func conditionText(v *jsonValue) string {
	if v == nil || v.kind != jsonObject {
		return shortJSON(v)
	}
	if c, ok := v.get("condition"); ok && c.kind == jsonString {
		return c.str
	}
	return shortJSON(v)
}

func compareCondition(kind string, enName, jaName string, old, new *jsonValue, changes *[]change) {
	enAction, jaAction := "shown", "見せる"
	if kind == "required" {
		enAction, jaAction = "required", "必須にする"
	}
	switch {
	case old == nil || old.kind == jsonNull:
		addChange(changes, "form",
			sprintf("The field %s gains a condition that makes it %s: %s.", enName, enAction, conditionText(new)),
			sprintf("項目%sに、%s条件が追加されます。%sです。", jaName, jaAction, conditionText(new)),
			false)
	case new == nil || new.kind == jsonNull:
		addChange(changes, "form",
			sprintf("The field %s loses the condition that made it %s: %s.", enName, enAction, conditionText(old)),
			sprintf("項目%sの、%s条件がなくなります。%sでした。", jaName, jaAction, conditionText(old)),
			false)
	case conditionText(old) != conditionText(new):
		addChange(changes, "form",
			sprintf("The condition that makes the field %s %s changes from %s to %s.", enName, enAction, conditionText(old), conditionText(new)),
			sprintf("項目%sを%s条件が、%sから%sに変わります。", jaName, jaAction, conditionText(old), conditionText(new)),
			false)
	default:
		addChange(changes, "form",
			sprintf("How the condition that makes the field %s %s is worked out changes; the condition stays %s.", enName, enAction, conditionText(new)),
			sprintf("項目%sを%s条件の計算の仕方が変わります。条件は%sのままです。", jaName, jaAction, conditionText(new)),
			false)
	}
}

func compareStages(old, new *jsonValue, od, nd *descriptionFile, changes *[]change) {
	if (old != nil && old.kind != jsonArray) || (new != nil && new.kind != jsonArray) {
		compareOtherKey("stages", old, new, changes)
		return
	}
	oldArr, newArr := formItems(old), formItems(new)
	oldOrder, oldItems, oldPos := keyedOrder(oldArr)
	newOrder, newItems, newPos := keyedOrder(newArr)
	moved := movedInSequence(oldOrder, newOrder)
	for _, name := range unionOrder(oldOrder, newOrder) {
		ov, inOld := oldItems[name]
		nv, inNew := newItems[name]
		displayOf := nv
		displaySide := nd
		if !inNew {
			displayOf = ov
			displaySide = od
		}
		enName := displayNamed(displayOf, "en", displaySide.language, name)
		jaName := displayNamed(displayOf, "ja", displaySide.language, name)
		switch {
		case inOld && !inNew:
			addChange(changes, "stages",
				sprintf("The stage %s is removed.", enName),
				sprintf("ステップ%sがなくなります。", jaName),
				false)
			continue
		case !inOld && inNew:
			addChange(changes, "stages",
				sprintf("The stage %s is added.", enName),
				sprintf("ステップ%sが追加されます。", jaName),
				false)
			continue
		}
		if moved[name] {
			addChange(changes, "stages",
				sprintf("The stage %s moves from position %d to position %d.", enName, oldPos[name], newPos[name]),
				sprintf("ステップ%sの位置が%d番目から%d番目に変わります。", jaName, oldPos[name], newPos[name]),
				false)
		}
		compareStage(ov, nv, enName, jaName, changes)
	}
}

// stageKeys is every key a stage's description has today, and how it is compared. Any other key
// is reported in "other" as an unknown value, never guessed to be one of these.
var stageKeys = map[string]string{
	"written":          "metadata",
	"label":            "metadata",
	"labels":           "metadata",
	"comment_required": "comment_required",
	"approvers":        "approvers",
	"deadlines":        "deadlines",
}

func compareStage(old, new *jsonValue, enName, jaName string, changes *[]change) {
	for _, key := range keyUnion(old, new) {
		if key == "name" {
			continue
		}
		ov, _ := old.get(key)
		nv, _ := new.get(key)
		if sameJSON(ov, nv) {
			continue
		}
		switch stageKeys[key] {
		case "metadata":
			compareMetadataValue("stages", enName, jaName, "stage", "ステップ", key, ov, nv, changes)
		case "comment_required":
			compareCommentRequired(enName, jaName, ov, nv, changes)
		case "approvers":
			compareApproverCases(enName, jaName, ov, nv, changes)
		case "deadlines":
			compareDeadlineLines(enName, jaName, ov, nv, changes)
		default:
			compareUnknownNestedValue(enName, jaName, "stage", "ステップ", key, ov, nv, changes)
		}
	}
}

func metadataWords(key string) (string, string) {
	switch key {
	case "label":
		return "label", "ラベル"
	case "labels":
		return "labels in other languages", "ほかの言語のラベル"
	case "written":
		return "name as written", "書かれたままの名前"
	}
	return key, keyWord(key)
}

func compareMetadataValue(section, enOwner, jaOwner, enKind, jaKind, key string, old, new *jsonValue, changes *[]change) {
	enWhat, jaWhat := metadataWords(key)
	if old == nil {
		addChange(changes, section,
			sprintf("The %s %s gains a %s: %s.", enKind, enOwner, enWhat, valueWord("en", new)),
			sprintf("%s%sに%sとして%sが加わります。", jaKind, jaOwner, jaWhat, valueWord("ja", new)),
			false)
		return
	}
	if new == nil {
		addChange(changes, section,
			sprintf("The %s %s loses its %s: it was %s.", enKind, enOwner, enWhat, valueWord("en", old)),
			sprintf("%s%sの%sがなくなります。これまでは%sでした。", jaKind, jaOwner, jaWhat, valueWord("ja", old)),
			false)
		return
	}
	addChange(changes, section,
		sprintf("The %s of the %s %s changes from %s to %s.", enWhat, enKind, enOwner, valueWord("en", old), valueWord("en", new)),
		sprintf("%s%sの%sが%sから%sに変わります。", jaKind, jaOwner, jaWhat, valueWord("ja", old), valueWord("ja", new)),
		false)
}

func compareUnknownNestedValue(enOwner, jaOwner, enKind, jaKind, key string, old, new *jsonValue, changes *[]change) {
	if old == nil {
		addChange(changes, "other",
			sprintf("The %s of the %s %s is added: %s.", keyWord(key), enKind, enOwner, valueWord("en", new)),
			sprintf("%s%sの%sが追加されます。%sです。", jaKind, jaOwner, keyWord(key), valueWord("ja", new)),
			false)
		return
	}
	if new == nil {
		addChange(changes, "other",
			sprintf("The %s of the %s %s is removed: it was %s.", keyWord(key), enKind, enOwner, valueWord("en", old)),
			sprintf("%s%sの%sがなくなります。これまでは%sでした。", jaKind, jaOwner, keyWord(key), valueWord("ja", old)),
			false)
		return
	}
	addChange(changes, "other",
		sprintf("The %s of the %s %s changes from %s to %s.", keyWord(key), enKind, enOwner, valueWord("en", old), valueWord("en", new)),
		sprintf("%s%sの%sが%sから%sに変わります。", jaKind, jaOwner, keyWord(key), valueWord("ja", old), valueWord("ja", new)),
		false)
}

func stringsOf(v *jsonValue) []string {
	if v == nil || v.kind != jsonArray {
		return nil
	}
	out := make([]string, 0, len(v.arr))
	for _, x := range v.arr {
		if x.kind == jsonString {
			out = append(out, x.str)
		}
	}
	return out
}

func compareCommentRequired(enName, jaName string, old, new *jsonValue, changes *[]change) {
	oldList, newList := stringsOf(old), stringsOf(new)
	for _, event := range oldList {
		if !slices.Contains(newList, event) {
			addChange(changes, "stages",
				sprintf("The stage %s no longer requires a reason for `%s`.", enName, event),
				sprintf("ステップ%sでは、`%s`に理由が要らなくなります。", jaName, event),
				false)
		}
	}
	for _, event := range newList {
		if !slices.Contains(oldList, event) {
			addChange(changes, "stages",
				sprintf("The stage %s now requires a reason for `%s`.", enName, event),
				sprintf("ステップ%sでは、`%s`に理由が必要になります。", jaName, event),
				false)
		}
	}
}

func compareWays(old, new *jsonValue, od, nd *descriptionFile, changes *[]change) {
	if (old != nil && old.kind != jsonArray) || (new != nil && new.kind != jsonArray) {
		compareOtherKey("ways", old, new, changes)
		return
	}
	oldArr, newArr := formItems(old), formItems(new)
	oldOrder, oldItems, oldPos := keyedOrder(oldArr)
	newOrder, newItems, newPos := keyedOrder(newArr)
	moved := movedInSequence(oldOrder, newOrder)
	for _, name := range unionOrder(oldOrder, newOrder) {
		ov, inOld := oldItems[name]
		nv, inNew := newItems[name]
		displayOf := nv
		displaySide := nd
		if !inNew {
			displayOf = ov
			displaySide = od
		}
		enName := displayNamed(displayOf, "en", displaySide.language, name)
		jaName := displayNamed(displayOf, "ja", displaySide.language, name)
		switch {
		case inOld && !inNew:
			addChange(changes, "approvers",
				sprintf("The way to choose approvers %s is removed.", enName),
				sprintf("承認者の決め方%sがなくなります。", jaName),
				false)
			continue
		case !inOld && inNew:
			addChange(changes, "approvers",
				sprintf("The way to choose approvers %s is added.", enName),
				sprintf("承認者の決め方%sが追加されます。", jaName),
				false)
			continue
		}
		if moved[name] {
			addChange(changes, "approvers",
				sprintf("The way to choose approvers %s moves from position %d to position %d.", enName, oldPos[name], newPos[name]),
				sprintf("承認者の決め方%sの位置が%d番目から%d番目に変わります。", jaName, oldPos[name], newPos[name]),
				false)
		}
		compareWay(ov, nv, enName, jaName, changes)
	}
}

// wayKeys is every key a way's description has today. As with stages, an unknown key is not
// interpreted as the rule itself.
var wayKeys = map[string]string{
	"written": "metadata",
	"label":   "metadata",
	"labels":  "metadata",
	"how":     "how",
}

func compareWay(old, new *jsonValue, enName, jaName string, changes *[]change) {
	for _, key := range keyUnion(old, new) {
		if key == "name" {
			continue
		}
		ov, _ := old.get(key)
		nv, _ := new.get(key)
		if sameJSON(ov, nv) {
			continue
		}
		if key == "how" {
			compareWayHow(enName, jaName, ov, nv, changes)
		} else if wayKeys[key] == "metadata" {
			compareMetadataValue("approvers", enName, jaName, "way to choose approvers", "承認者の決め方", key, ov, nv, changes)
		} else {
			compareUnknownNestedValue(enName, jaName, "way to choose approvers", "承認者の決め方", key, ov, nv, changes)
		}
	}
}

func compareSelfApproval(old, new *jsonValue, changes *[]change) {
	if old == nil || new == nil {
		compareOtherKey("self_approval", old, new, changes)
		return
	}
	addChange(changes, "approvers",
		sprintf("Self approval changes from %s to %s.", boolWord("en", old.boolOr(false)), boolWord("en", new.boolOr(false))),
		sprintf("自己承認は、%sから%sに変わります。", boolWord("ja", old.boolOr(false)), boolWord("ja", new.boolOr(false))),
		false)
}

func findField(root *jsonValue, name string) *jsonValue {
	if root == nil {
		return nil
	}
	form, ok := root.get("form")
	if !ok || form.kind != jsonArray {
		return nil
	}
	var walk func([]*jsonValue) *jsonValue
	walk = func(fields []*jsonValue) *jsonValue {
		for _, f := range fields {
			if n, _ := f.get("name"); n.strOr("") == name {
				return f
			}
			if children, ok := f.get("fields"); ok && children.kind == jsonArray {
				if found := walk(children.arr); found != nil {
					return found
				}
			}
		}
		return nil
	}
	return walk(form.arr)
}

func compareApproversEdit(old, new *jsonValue, od, nd *descriptionFile, changes *[]change) {
	oldList, newList := stringsOf(old), stringsOf(new)
	for _, name := range oldList {
		if slices.Contains(newList, name) {
			continue
		}
		field := findField(od.root, name)
		enName, jaName := name, name
		if field != nil {
			enName = displayNamed(field, "en", od.language, name)
			jaName = displayNamed(field, "ja", od.language, name)
		}
		addChange(changes, "approvers",
			sprintf("Approvers may no longer edit the field %s.", enName),
			sprintf("承認者が直す項目から%sが外れます。", jaName),
			false)
	}
	for _, name := range newList {
		if slices.Contains(oldList, name) {
			continue
		}
		field := findField(nd.root, name)
		enName, jaName := name, name
		if field != nil {
			enName = displayNamed(field, "en", nd.language, name)
			jaName = displayNamed(field, "ja", nd.language, name)
		}
		addChange(changes, "approvers",
			sprintf("Approvers may now edit the field %s.", enName),
			sprintf("承認者が直す項目に%sが加わります。", jaName),
			false)
	}
	if sameStringSet(oldList, newList) && !slices.Equal(oldList, newList) {
		addChange(changes, "approvers",
			"The order of the fields approvers may edit changes.",
			"承認者が直す項目の順が変わります。",
			false)
	}
}

func compareEffectiveFrom(old, new *jsonValue, changes *[]change) {
	switch {
	case old == nil || old.kind == jsonNull:
		addChange(changes, "usable",
			sprintf("This version now takes effect from %s.", dayWord(new)),
			sprintf("この版は%sから効きます。", dayWord(new)),
			false)
	case new == nil || new.kind == jsonNull:
		addChange(changes, "usable",
			sprintf("This version no longer has a day it takes effect from; it was %s.", dayWord(old)),
			sprintf("この版が効き始める日はなくなります。これまでは%sでした。", dayWord(old)),
			false)
	default:
		addChange(changes, "usable",
			sprintf("The day this version takes effect changes from %s to %s.", dayWord(old), dayWord(new)),
			sprintf("この版が効き始める日が%sから%sに変わります。", dayWord(old), dayWord(new)),
			false)
	}
}

func explanationWord(lang string, v *jsonValue) string {
	if v == nil {
		return valueWord(lang, v)
	}
	if v.kind != jsonArray {
		if v.kind == jsonObject {
			if text, ok := v.get("text"); ok && text.kind == jsonString {
				return textWord(lang, text.str)
			}
		}
		return valueWord(lang, v)
	}
	parts := make([]string, 0, len(v.arr))
	for _, x := range v.arr {
		if x.kind == jsonObject {
			if text, ok := x.get("text"); ok && text.kind == jsonString {
				parts = append(parts, textWord(lang, text.str))
				continue
			}
		}
		parts = append(parts, valueWord(lang, x))
	}
	return strings.Join(parts, ", ")
}

func compareUsable(old, new *jsonValue, changes *[]change) {
	if old == nil && new == nil {
		return
	}
	if (old != nil && old.kind != jsonObject) || (new != nil && new.kind != jsonObject) {
		compareOtherKey("usable", old, new, changes)
		return
	}
	for _, key := range keyUnion(old, new) {
		ov, _ := old.get(key)
		nv, _ := new.get(key)
		if sameJSON(ov, nv) {
			continue
		}
		switch key {
		case "from":
			compareUsableDay(true, ov, nv, changes)
		case "through":
			compareUsableDay(false, ov, nv, changes)
		case "from_by":
			addChange(changes, "usable",
				sprintf("The data deciding the first day the kind can be used changes from %s to %s.", explanationWord("en", ov), explanationWord("en", nv)),
				sprintf("申請を出せる最初の日を決めるデータが%sから%sに変わります。", explanationWord("ja", ov), explanationWord("ja", nv)),
				false)
		case "through_by":
			addChange(changes, "usable",
				sprintf("The data deciding the last day the kind can be used changes from %s to %s.", explanationWord("en", ov), explanationWord("en", nv)),
				sprintf("申請を出せる最後の日を決めるデータが%sから%sに変わります。", explanationWord("ja", ov), explanationWord("ja", nv)),
				false)
		default:
			addChange(changes, "usable",
				sprintf("The %s of the usable days changes from %s to %s.", keyWord(key), valueWord("en", ov), valueWord("en", nv)),
				sprintf("申請を出せる日の%sが%sから%sに変わります。", keyWord(key), valueWord("ja", ov), valueWord("ja", nv)),
				false)
		}
	}
}

func compareUsableDay(first bool, old, new *jsonValue, changes *[]change) {
	enWhich, jaWhich := "first", "最初"
	if !first {
		enWhich, jaWhich = "last", "最後"
	}
	switch {
	case old == nil || old.kind == jsonNull:
		enText := sprintf("The kind can now be used only from %s.", dayWord(new))
		if !first {
			enText = sprintf("The kind can now be used only through %s.", dayWord(new))
		}
		addChange(changes, "usable",
			enText,
			sprintf("この申請の種類を出せる%sの日が%sに決まります。", jaWhich, dayWord(new)),
			false)
	case new == nil || new.kind == jsonNull:
		addChange(changes, "usable",
			sprintf("The kind no longer has a %s day it can be used through; the day was %s.", enWhich, dayWord(old)),
			sprintf("この申請の種類を出せる%sの日はなくなります。これまでは%sでした。", jaWhich, dayWord(old)),
			false)
	default:
		addChange(changes, "usable",
			sprintf("The %s day the kind can be used changes from %s to %s.", enWhich, dayWord(old), dayWord(new)),
			sprintf("この申請の種類を出せる%sの日が%sから%sに変わります。", jaWhich, dayWord(old), dayWord(new)),
			false)
	}
}

func compareUTCOffset(old, new *jsonValue, changes *[]change) {
	if old == nil || new == nil {
		compareOtherKey("utc_offset_minutes", old, new, changes)
		return
	}
	addChange(changes, "usable",
		sprintf("The UTC offset the kind reads its days in changes from %s to %s minutes.", old.compact(), new.compact()),
		sprintf("日付を読むUTCからの差が%s分から%s分に変わります。", old.compact(), new.compact()),
		false)
}

func compareStringSet(key string, old, new *jsonValue, changes *[]change) {
	oldList, newList := stringsOf(old), stringsOf(new)
	singularEn, pluralJa := "ending", "終わり方"
	if key == "events" {
		singularEn, pluralJa = "event", "操作"
	}
	for _, x := range oldList {
		if !slices.Contains(newList, x) {
			addChange(changes, "other",
				sprintf("The %s `%s` is removed.", singularEn, x),
				sprintf("%sの`%s`がなくなります。", pluralJa, x),
				false)
		}
	}
	for _, x := range newList {
		if !slices.Contains(oldList, x) {
			addChange(changes, "other",
				sprintf("The %s `%s` is added.", singularEn, x),
				sprintf("%sの`%s`が追加されます。", pluralJa, x),
				false)
		}
	}
	if sameStringSet(oldList, newList) && !slices.Equal(oldList, newList) {
		addChange(changes, "other",
			sprintf("The order of the %s changes.", key),
			sprintf("%sの順が変わります。", pluralJa),
			false)
	}
}

func submitGroupWord(lang string, v *jsonValue) string {
	if v == nil || v.kind != jsonObject {
		return valueWord(lang, v)
	}
	kind, _ := v.get("kind")
	team, _ := v.get("team")
	position, _ := v.get("position")
	higher, _ := v.get("higher")
	within, _ := v.get("within")
	person, _ := v.get("person")
	k := kind.strOr("")
	if lang == "ja" {
		switch k {
		case "everyone":
			if within != nil && within.kind == jsonString {
				return "チーム`" + within.str + "`とその下のチームの全員"
			}
			return "組織の全員"
		case "team":
			return "チーム`" + team.strOr("") + "`の人"
		case "position":
			out := "役職`" + position.strOr("") + "`"
			if higher != nil && higher.kind == jsonBool && higher.boolean {
				out += "以上"
			}
			out += "の人"
			if within != nil && within.kind == jsonString {
				out += "（チーム`" + within.str + "`とその下）"
			}
			return out
		case "person":
			return "人`" + person.strOr("") + "`"
		}
		return valueWord(lang, v)
	}
	switch k {
	case "everyone":
		if within != nil && within.kind == jsonString {
			return "everyone in team `" + within.str + "`"
		}
		return "everyone"
	case "team":
		return "the people of team `" + team.strOr("") + "`"
	case "position":
		out := "the people of position `" + position.strOr("") + "`"
		if higher != nil && higher.kind == jsonBool && higher.boolean {
			out += " or higher"
		}
		if within != nil && within.kind == jsonString {
			out += " in team `" + within.str + "`"
		}
		return out
	case "person":
		return "person `" + person.strOr("") + "`"
	}
	return valueWord(lang, v)
}

func submitGroupsWord(lang string, v *jsonValue) string {
	if v == nil || v.kind != jsonArray {
		return valueWord(lang, v)
	}
	parts := make([]string, 0, len(v.arr))
	for _, x := range v.arr {
		parts = append(parts, submitGroupWord(lang, x))
	}
	return strings.Join(parts, ", ")
}

func compareSubmittedBy(old, new *jsonValue, changes *[]change) {
	switch {
	case old == nil || old.kind == jsonNull:
		addChange(changes, "approvers",
			sprintf("A restriction on who may submit the kind is added: %s.", valueWord("en", new)),
			sprintf("出せる人の制限が追加されます。%sです。", valueWord("ja", new)),
			false)
	case new == nil || new.kind == jsonNull:
		addChange(changes, "approvers",
			"The restriction on who may submit the kind is removed; everyone in the organization may submit it.",
			"出せる人の制限がなくなり、組織の全員が出せるようになります。",
			false)
	default:
		for _, key := range keyUnion(old, new) {
			ov, _ := old.get(key)
			nv, _ := new.get(key)
			if sameJSON(ov, nv) {
				continue
			}
			switch key {
			case "groups":
				addChange(changes, "approvers",
					sprintf("The groups who may submit change from %s to %s.", submitGroupsWord("en", ov), submitGroupsWord("en", nv)),
					sprintf("出せるグループが%sから%sに変わります。", submitGroupsWord("ja", ov), submitGroupsWord("ja", nv)),
					false)
			case "except":
				addChange(changes, "approvers",
					sprintf("The people left out change from %s to %s.", submitGroupsWord("en", ov), submitGroupsWord("en", nv)),
					sprintf("出せる人から除く人が%sから%sに変わります。", submitGroupsWord("ja", ov), submitGroupsWord("ja", nv)),
					false)
			default:
				addChange(changes, "other",
					sprintf("The %s of who may submit changes from %s to %s.", keyWord(key), valueWord("en", ov), valueWord("en", nv)),
					sprintf("出せる人の%sが%sから%sに変わります。", keyWord(key), valueWord("ja", ov), valueWord("ja", nv)),
					false)
			}
		}
	}
}

func compareOrganization(old, new *jsonValue, od, nd *descriptionFile, changes *[]change) {
	if old == nil || new == nil || old.kind != jsonObject || new.kind != jsonObject {
		compareOtherKey("organization", old, new, changes)
		return
	}
	for _, key := range keyUnion(old, new) {
		ov, _ := old.get(key)
		nv, _ := new.get(key)
		if sameJSON(ov, nv) {
			continue
		}
		switch key {
		case "positions":
			compareOrganizationNamedList("position", "役職", ov, nv, od, nd, changes)
		case "team_kinds":
			compareOrganizationNamedList("team kind", "チームの種類", ov, nv, od, nd, changes)
		case "needs":
			compareOrganizationNeeds(ov, nv, changes)
		default:
			enWhat := map[string]string{"file": "the file of the organization", "name": "the organization's name", "title": "the organization's title", "data": "where the organization's data is"}[key]
			jaWhat := map[string]string{"file": "組織のファイル", "name": "組織の名前", "title": "組織の題", "data": "組織のデータの場所"}[key]
			if enWhat == "" {
				enWhat, jaWhat = "the "+keyWord(key), keyWord(key)
			}
			addChange(changes, "other",
				sprintf("%s changes from %s to %s.", enWhat, valueWord("en", ov), valueWord("en", nv)),
				sprintf("%sが%sから%sに変わります。", jaWhat, valueWord("ja", ov), valueWord("ja", nv)),
				false)
		}
	}
}

func compareOrganizationNamedList(enKind, jaKind string, old, new *jsonValue, od, nd *descriptionFile, changes *[]change) {
	if (old != nil && old.kind != jsonArray) || (new != nil && new.kind != jsonArray) {
		addChange(changes, "approvers",
			sprintf("The %ss of the organization change from %s to %s.", enKind, valueWord("en", old), valueWord("en", new)),
			sprintf("組織の%sが%sから%sに変わります。", jaKind, valueWord("ja", old), valueWord("ja", new)),
			false)
		return
	}
	oldArr, newArr := formItems(old), formItems(new)
	oldOrder, oldItems, oldPos := keyedOrder(oldArr)
	newOrder, newItems, newPos := keyedOrder(newArr)
	moved := movedInSequence(oldOrder, newOrder)
	for _, name := range unionOrder(oldOrder, newOrder) {
		ov, inOld := oldItems[name]
		nv, inNew := newItems[name]
		displayOf := nv
		displaySide := nd
		if !inNew {
			displayOf = ov
			displaySide = od
		}
		enName := displayNamed(displayOf, "en", displaySide.language, name)
		jaName := displayNamed(displayOf, "ja", displaySide.language, name)
		switch {
		case inOld && !inNew:
			addChange(changes, "approvers",
				sprintf("The %s %s is removed.", enKind, enName),
				sprintf("%s%sがなくなります。", jaKind, jaName),
				false)
			continue
		case !inOld && inNew:
			addChange(changes, "approvers",
				sprintf("The %s %s is added.", enKind, enName),
				sprintf("%s%sが追加されます。", jaKind, jaName),
				false)
			continue
		}
		if moved[name] {
			addChange(changes, "approvers",
				sprintf("The %s %s moves from position %d to position %d.", enKind, enName, oldPos[name], newPos[name]),
				sprintf("%s%sの位置が%d番目から%d番目に変わります。", jaKind, jaName, oldPos[name], newPos[name]),
				false)
		}
		for _, key := range keyUnion(ov, nv) {
			if key == "name" {
				continue
			}
			ovv, _ := ov.get(key)
			nvv, _ := nv.get(key)
			if sameJSON(ovv, nvv) {
				continue
			}
			if key == "written" || key == "label" || key == "labels" {
				compareMetadataValue("approvers", enName, jaName, enKind, jaKind, key, ovv, nvv, changes)
			} else {
				compareUnknownNestedValue(enName, jaName, enKind, jaKind, key, ovv, nvv, changes)
			}
		}
	}
}

func compareOrganizationNeeds(old, new *jsonValue, changes *[]change) {
	if old == nil || new == nil || old.kind != jsonObject || new.kind != jsonObject {
		addChange(changes, "approvers",
			sprintf("What the kind needs of the organization changes from %s to %s.", valueWord("en", old), valueWord("en", new)),
			sprintf("この申請の種類が組織から読む部分が%sから%sに変わります。", valueWord("ja", old), valueWord("ja", new)),
			false)
		return
	}
	words := map[string][2]string{
		"people":     {"the people the kind needs of the organization", "組織から読む人"},
		"teams":      {"the teams the kind needs of the organization", "組織から読むチーム"},
		"members_of": {"the teams whose members the kind needs", "所属する人を読むチーム"},
	}
	for _, key := range keyUnion(old, new) {
		ov, _ := old.get(key)
		nv, _ := new.get(key)
		if sameJSON(ov, nv) {
			continue
		}
		w := words[key]
		if w[0] == "" {
			addChange(changes, "other",
				sprintf("The %s the kind needs of the organization changes from %s to %s.", keyWord(key), valueWord("en", ov), valueWord("en", nv)),
				sprintf("組織から読む%sが%sから%sに変わります。", keyWord(key), valueWord("ja", ov), valueWord("ja", nv)),
				false)
			continue
		}
		compareNeedStrings(key, w[0], w[1], ov, nv, changes)
	}
}

func compareNeedStrings(key, enWhat, jaWhat string, old, new *jsonValue, changes *[]change) {
	oldList, newList := stringsOf(old), stringsOf(new)
	for _, x := range oldList {
		if !slices.Contains(newList, x) {
			addChange(changes, "approvers",
				sprintf("`%s` is removed from %s.", x, enWhat),
				sprintf("%sから`%s`がなくなります。", jaWhat, x),
				false)
		}
	}
	for _, x := range newList {
		if !slices.Contains(oldList, x) {
			addChange(changes, "approvers",
				sprintf("`%s` is added to %s.", x, enWhat),
				sprintf("%sに`%s`が追加されます。", jaWhat, x),
				false)
		}
	}
	if sameStringSet(oldList, newList) && !slices.Equal(oldList, newList) {
		addChange(changes, "approvers",
			sprintf("The order of %s changes.", enWhat),
			sprintf("%sの順が変わります。", jaWhat),
			false)
	}
}

func compareMasters(old, new *jsonValue, od, nd *descriptionFile, changes *[]change) {
	if (old != nil && old.kind != jsonArray) || (new != nil && new.kind != jsonArray) {
		compareOtherKey("masters", old, new, changes)
		return
	}
	oldArr, newArr := formItems(old), formItems(new)
	oldOrder, oldItems, _ := keyedOrder(oldArr)
	newOrder, newItems, _ := keyedOrder(newArr)
	for _, name := range unionOrder(oldOrder, newOrder) {
		ov, inOld := oldItems[name]
		nv, inNew := newItems[name]
		displayOf := nv
		displaySide := nd
		if !inNew {
			displayOf = ov
			displaySide = od
		}
		enName := displayNamed(displayOf, "en", displaySide.language, name)
		jaName := displayNamed(displayOf, "ja", displaySide.language, name)
		switch {
		case inOld && !inNew:
			addChange(changes, "other",
				sprintf("The master %s is no longer read.", enName),
				sprintf("マスタ%sは読まなくなります。", jaName),
				false)
		case !inOld && inNew:
			addChange(changes, "other",
				sprintf("The master %s is now read.", enName),
				sprintf("マスタ%sを読むようになります。", jaName),
				false)
		default:
			compareMaster(ov, nv, name, enName, jaName, od, nd, changes)
		}
	}
}

func compareMaster(old, new *jsonValue, name, enName, jaName string, od, nd *descriptionFile, changes *[]change) {
	for _, key := range keyUnion(old, new) {
		if key == "name" {
			continue
		}
		ov, _ := old.get(key)
		nv, _ := new.get(key)
		if sameJSON(ov, nv) {
			continue
		}
		if key == "fields" {
			before := len(*changes)
			compareFieldList(formItems(ov), formItems(nv), name, od, nd, changes)
			for i := before; i < len(*changes); i++ {
				(*changes)[i].section = "other"
			}
			continue
		}
		addChange(changes, "other",
			sprintf("The %s of the master %s changes from %s to %s.", keyWord(key), enName, valueWord("en", ov), valueWord("en", nv)),
			sprintf("マスタ%sの%sが%sから%sに変わります。", jaName, keyWord(key), valueWord("ja", ov), valueWord("ja", nv)),
			false)
	}
}

func compareFlows(old, new *jsonValue, changes *[]change) {
	if (old != nil && old.kind != jsonArray) || (new != nil && new.kind != jsonArray) {
		compareOtherKey("flows", old, new, changes)
		return
	}
	oldArr, newArr := formItems(old), formItems(new)
	keyOf := func(item *jsonValue, index int) string {
		if item != nil && item.kind == jsonObject {
			if n, ok := item.get("alias"); ok && n.kind == jsonString && n.str != "" {
				return n.str
			}
		}
		return fieldName(item, index)
	}
	orders := func(arr []*jsonValue) ([]string, map[string]*jsonValue) {
		order := make([]string, len(arr))
		items := map[string]*jsonValue{}
		for i, item := range arr {
			name := keyOf(item, i)
			if _, ok := items[name]; ok {
				name += "#" + strconv.Itoa(i+1)
			}
			order[i] = name
			items[name] = item
		}
		return order, items
	}
	oldOrder, oldItems := orders(oldArr)
	newOrder, newItems := orders(newArr)
	for _, name := range unionOrder(oldOrder, newOrder) {
		ov, inOld := oldItems[name]
		nv, inNew := newItems[name]
		switch {
		case inOld && !inNew:
			addChange(changes, "other",
				sprintf("The flow `%s` is no longer used.", name),
				sprintf("flowの`%s`は使わなくなります。", name),
				false)
		case !inOld && inNew:
			addChange(changes, "other",
				sprintf("The flow `%s` is now used.", name),
				sprintf("flowの`%s`を使うようになります。", name),
				false)
		default:
			for _, key := range keyUnion(ov, nv) {
				ovv, _ := ov.get(key)
				nvv, _ := nv.get(key)
				if sameJSON(ovv, nvv) {
					continue
				}
				addChange(changes, "other",
					sprintf("The %s of the flow `%s` changes from %s to %s.", keyWord(key), name, valueWord("en", ovv), valueWord("en", nvv)),
					sprintf("flowの`%s`の%sが%sから%sに変わります。", name, keyWord(key), valueWord("ja", ovv), valueWord("ja", nvv)),
					false)
			}
		}
	}
}

func compareOtherKey(key string, old, new *jsonValue, changes *[]change) {
	if sameJSON(old, new) {
		return
	}
	if old == nil {
		addChange(changes, "other",
			sprintf("The %s of the kind is added: %s.", keyWord(key), valueWord("en", new)),
			sprintf("申請の種類の%sが追加されます。%sです。", keyWord(key), valueWord("ja", new)),
			false)
		return
	}
	if new == nil {
		addChange(changes, "other",
			sprintf("The %s of the kind is removed: it was %s.", keyWord(key), valueWord("en", old)),
			sprintf("申請の種類の%sがなくなります。これまでは%sでした。", keyWord(key), valueWord("ja", old)),
			false)
		return
	}
	addChange(changes, "other",
		sprintf("The %s of the kind changes from %s to %s.", keyWord(key), valueWord("en", old), valueWord("en", new)),
		sprintf("申請の種類の%sが%sから%sに変わります。", keyWord(key), valueWord("ja", old), valueWord("ja", new)),
		false)
}

func bilingualWord(v *jsonValue, lang string) string {
	if v == nil || v.kind == jsonNull {
		if lang == "ja" {
			return "いつも"
		}
		return "always"
	}
	if v.kind == jsonObject {
		if x, ok := v.get(lang); ok && x.kind == jsonString {
			return x.str
		}
		if x, ok := v.get("en"); ok && x.kind == jsonString {
			return x.str
		}
	}
	return shortJSON(v)
}

// quotedBilingualWord quotes the sentence the description holds. Null is not a sentence of the
// description: it is the unconditional case, so the generated word for that is left unquoted.
func quotedBilingualWord(v *jsonValue, lang string) string {
	if v == nil || v.kind == jsonNull {
		return bilingualWord(v, lang)
	}
	return textWord(lang, bilingualWord(v, lang))
}

// machineWord writes a machine value of the contract, such as a way's ASCII name, distinctly
// from the sentences written for people.
func machineWord(lang string, v *jsonValue) string {
	if v == nil || v.kind == jsonNull {
		if lang == "ja" {
			return "なし"
		}
		return "nothing"
	}
	if v.kind == jsonString {
		return "`" + v.str + "`"
	}
	if v.kind == jsonArray {
		if len(v.arr) == 0 {
			if lang == "ja" {
				return "なし"
			}
			return "nothing"
		}
		parts := make([]string, 0, len(v.arr))
		for _, x := range v.arr {
			if x.kind == jsonString {
				parts = append(parts, "`"+x.str+"`")
			} else {
				parts = append(parts, valueWord(lang, x))
			}
		}
		return strings.Join(parts, ", ")
	}
	return valueWord(lang, v)
}

// caseSummary names one case by the two sentences the pages show. The condition comes first in
// both languages here so that the same value is the name of the case in addition, removal and
// movement lines.
func caseSummary(v *jsonValue, lang string) string {
	if v == nil || v.kind != jsonObject {
		return shortJSON(v)
	}
	who, _ := v.get("who")
	when, _ := v.get("when")
	whoWord := textWord(lang, bilingualWord(who, lang))
	if when == nil || when.kind == jsonNull {
		if lang == "ja" {
			return whoWord + "（いつも）"
		}
		return whoWord + " (always)"
	}
	whenWord := textWord(lang, bilingualWord(when, lang))
	if lang == "ja" {
		return whenWord + "、" + whoWord
	}
	return whenWord + ", " + whoWord
}

func caseFacts(v *jsonValue, lang string) string {
	if v == nil || v.kind != jsonObject {
		return ""
	}
	var parts []string
	kind, _ := v.get("kind")
	if kind != nil && kind.kind == jsonString && kind.str != "" {
		if lang == "ja" {
			parts = append(parts, "種類`"+kind.str+"`")
		} else {
			parts = append(parts, "kind `"+kind.str+"`")
		}
	}
	ways, _ := v.get("ways")
	if ways != nil && ways.kind == jsonArray && len(ways.arr) != 0 {
		if lang == "ja" {
			parts = append(parts, "決め方"+machineWord(lang, ways))
		} else {
			parts = append(parts, "ways "+machineWord(lang, ways))
		}
	}
	route, _ := v.get("route")
	if route != nil && route.kind == jsonString && route.str != "" {
		if lang == "ja" {
			parts = append(parts, "共有の経路`"+route.str+"`")
		} else {
			parts = append(parts, "route `"+route.str+"`")
		}
	} else if lang == "ja" {
		parts = append(parts, "共有の経路なし")
	} else {
		parts = append(parts, "no shared route")
	}
	if len(parts) == 0 {
		return ""
	}
	if lang == "ja" {
		return "（" + strings.Join(parts, "、") + "）"
	}
	return " (" + strings.Join(parts, "; ") + ")"
}

func currentCaseWords(v *jsonValue, lang string) string {
	return caseSummary(v, lang) + caseFacts(v, lang)
}

func contentFingerprint(v *jsonValue) string { return canonicalJSON(v) }

// compareKeyedByContent matches the entries written unchanged, so that an insertion does not
// make every later entry a change. Entries left unmatched are paired by the caller; they are
// either edited entries or a removal beside an addition.
func compareKeyedByContent(old, new *jsonValue) (removed, added, moved []*jsonValue) {
	oldArr, newArr := formItems(old), formItems(new)
	oldOrder := make([]string, len(oldArr))
	newOrder := make([]string, len(newArr))
	oldBy := map[string]*jsonValue{}
	newBy := map[string]*jsonValue{}
	for i, x := range oldArr {
		k := contentFingerprint(x)
		if _, ok := oldBy[k]; ok {
			k += "#" + strconv.Itoa(i)
		}
		oldOrder[i], oldBy[k] = k, x
	}
	for i, x := range newArr {
		k := contentFingerprint(x)
		if _, ok := newBy[k]; ok {
			k += "#" + strconv.Itoa(i)
		}
		newOrder[i], newBy[k] = k, x
	}
	movedKeys := movedInSequence(oldOrder, newOrder)
	inNew := map[string]bool{}
	for _, k := range newOrder {
		inNew[k] = true
	}
	inOld := map[string]bool{}
	for _, k := range oldOrder {
		inOld[k] = true
	}
	for _, k := range oldOrder {
		if !inNew[k] {
			removed = append(removed, oldBy[k])
		} else if movedKeys[k] {
			moved = append(moved, oldBy[k])
		}
	}
	for _, k := range newOrder {
		if !inOld[k] {
			added = append(added, newBy[k])
		}
	}
	return removed, added, moved
}

func nonNull(v *jsonValue) bool { return v != nil && v.kind != jsonNull }

func stringsOverlap(a, b *jsonValue) bool {
	as, bs := stringsOf(a), stringsOf(b)
	for _, x := range as {
		if slices.Contains(bs, x) {
			return true
		}
	}
	return false
}

// caseSimilarity is only a matcher for entries left after the exact matches. The threshold is
// low enough to pair one edited field and high enough not to pair two unrelated cases merely
// because both are ordinary `who` cases.
func caseSimilarity(a, b *jsonValue) int {
	score := 0
	aw, _ := a.get("when")
	bw, _ := b.get("when")
	if sameJSON(aw, bw) {
		if nonNull(aw) {
			score += 3
		} else {
			score++
		}
	} else if nonNull(aw) && nonNull(bw) {
		score++
	}
	ah, _ := a.get("who")
	bh, _ := b.get("who")
	if sameJSON(ah, bh) {
		score += 4
	}
	ays, _ := a.get("ways")
	bys, _ := b.get("ways")
	if sameJSON(ays, bys) {
		score += 3
	} else if stringsOverlap(ays, bys) {
		score++
	}
	ak, _ := a.get("kind")
	bk, _ := b.get("kind")
	if sameJSON(ak, bk) {
		score++
	}
	ar, _ := a.get("route")
	br, _ := b.get("route")
	if sameJSON(ar, br) && nonNull(ar) {
		score++
	}
	return score
}

type caseChangePair struct {
	old, new *jsonValue
	oi, ni   int
}

func pairChangedCases(removed, added []*jsonValue) (pairs []caseChangePair, leftOld, leftNew []*jsonValue) {
	usedOld := make([]bool, len(removed))
	usedNew := make([]bool, len(added))
	for {
		bestO, bestN, bestScore := -1, -1, 4
		for oi, ov := range removed {
			if usedOld[oi] {
				continue
			}
			for ni, nv := range added {
				if usedNew[ni] {
					continue
				}
				if score := caseSimilarity(ov, nv); score > bestScore {
					bestO, bestN, bestScore = oi, ni, score
				}
			}
		}
		if bestO < 0 {
			break
		}
		usedOld[bestO], usedNew[bestN] = true, true
		pairs = append(pairs, caseChangePair{old: removed[bestO], new: added[bestN], oi: bestO, ni: bestN})
	}
	for i := 1; i < len(pairs); i++ {
		for j := i; j > 0 && pairs[j].oi < pairs[j-1].oi; j-- {
			pairs[j], pairs[j-1] = pairs[j-1], pairs[j]
		}
	}
	for i, x := range removed {
		if !usedOld[i] {
			leftOld = append(leftOld, x)
		}
	}
	for i, x := range added {
		if !usedNew[i] {
			leftNew = append(leftNew, x)
		}
	}
	return pairs, leftOld, leftNew
}

func compareApproverCases(enStage, jaStage string, old, new *jsonValue, changes *[]change) {
	if (old != nil && old.kind != jsonArray) || (new != nil && new.kind != jsonArray) {
		compareUnknownNestedValue(enStage, jaStage, "stage", "ステップ", "approvers", old, new, changes)
		return
	}
	removed, added, moved := compareKeyedByContent(old, new)
	pairs, removed, added := pairChangedCases(removed, added)
	for _, pair := range pairs {
		compareApproverCase(enStage, jaStage, pair.old, pair.new, changes)
	}
	for _, x := range removed {
		addChange(changes, "approvers",
			sprintf("At the stage %s, the case %s is removed.", enStage, currentCaseWords(x, "en")),
			sprintf("ステップ%sでは、%sの場合がなくなります。", jaStage, currentCaseWords(x, "ja")), false)
	}
	for _, x := range added {
		addChange(changes, "approvers",
			sprintf("At the stage %s, the case %s is added.", enStage, currentCaseWords(x, "en")),
			sprintf("ステップ%sでは、%sの場合が追加されます。", jaStage, currentCaseWords(x, "ja")), false)
	}
	for _, x := range moved {
		addChange(changes, "approvers",
			sprintf("At the stage %s, the case %s moves in the route.", enStage, caseSummary(x, "en")),
			sprintf("ステップ%sでは、%sの場合の順が変わります。", jaStage, caseSummary(x, "ja")), false)
	}
}

func compareApproverCase(enStage, jaStage string, old, new *jsonValue, changes *[]change) {
	enCase, jaCase := caseSummary(old, "en"), caseSummary(old, "ja")
	for _, key := range []string{"when", "who", "ways", "kind", "route"} {
		ov, _ := old.get(key)
		nv, _ := new.get(key)
		if sameJSON(ov, nv) || key == "ways" && sameStringSet(stringsOf(ov), stringsOf(nv)) {
			continue
		}
		var enWhat, jaWhat, enFrom, enTo, jaFrom, jaTo string
		switch key {
		case "when":
			enWhat, jaWhat = "the condition under which it comes", "来るとき"
			enFrom, enTo = quotedBilingualWord(ov, "en"), quotedBilingualWord(nv, "en")
			jaFrom, jaTo = quotedBilingualWord(ov, "ja"), quotedBilingualWord(nv, "ja")
		case "who":
			enWhat, jaWhat = "who answers", "答える人"
			enFrom, enTo = quotedBilingualWord(ov, "en"), quotedBilingualWord(nv, "en")
			jaFrom, jaTo = quotedBilingualWord(ov, "ja"), quotedBilingualWord(nv, "ja")
		case "ways":
			enWhat, jaWhat = "the set of ways it names", "指す決め方"
			enFrom, enTo = machineWord("en", ov), machineWord("en", nv)
			jaFrom, jaTo = machineWord("ja", ov), machineWord("ja", nv)
		case "kind":
			enWhat, jaWhat = "the kind of the case", "場合の種類"
			enFrom, enTo = machineWord("en", ov), machineWord("en", nv)
			jaFrom, jaTo = machineWord("ja", ov), machineWord("ja", nv)
		case "route":
			enWhat, jaWhat = "the shared route it belongs to", "属する共有の経路"
			enFrom, enTo = machineWord("en", ov), machineWord("en", nv)
			jaFrom, jaTo = machineWord("ja", ov), machineWord("ja", nv)
		}
		addChange(changes, "approvers",
			sprintf("At the stage %s, in the case %s, %s changes from %s to %s.", enStage, enCase, enWhat, enFrom, enTo),
			sprintf("ステップ%sでは、%sの場合の%sが%sから%sに変わります。", jaStage, jaCase, jaWhat, jaFrom, jaTo), false)
	}
}

func deadlineWords(v *jsonValue, lang string) string {
	if v == nil || v.kind != jsonObject {
		return shortJSON(v)
	}
	if x, ok := v.get(lang); ok && x.kind == jsonString {
		return x.str
	}
	if x, ok := v.get("en"); ok && x.kind == jsonString {
		return x.str
	}
	return shortJSON(v)
}

func deadlineKind(v *jsonValue, index int) string {
	if v != nil && v.kind == jsonObject {
		if k, ok := v.get("kind"); ok && k.kind == jsonString && k.str != "" {
			return k.str
		}
	}
	return "#" + strconv.Itoa(index+1)
}

func keyedDeadlines(arr []*jsonValue) ([]string, map[string]*jsonValue) {
	order := make([]string, len(arr))
	items := map[string]*jsonValue{}
	seen := map[string]int{}
	for i, x := range arr {
		kind := deadlineKind(x, i)
		seen[kind]++
		key := kind
		if seen[kind] != 1 {
			key += "#" + strconv.Itoa(seen[kind])
		}
		order[i], items[key] = key, x
	}
	return order, items
}

func deadlineLineName(v *jsonValue) string {
	if v != nil && v.kind == jsonObject {
		if k, ok := v.get("kind"); ok && k.kind == jsonString && k.str != "" {
			return "`" + k.str + "`"
		}
	}
	return "`deadline`"
}

func compareDeadlineLines(enStage, jaStage string, old, new *jsonValue, changes *[]change) {
	if (old != nil && old.kind != jsonArray) || (new != nil && new.kind != jsonArray) {
		compareUnknownNestedValue(enStage, jaStage, "stage", "ステップ", "deadlines", old, new, changes)
		return
	}
	oldOrder, oldItems := keyedDeadlines(formItems(old))
	newOrder, newItems := keyedDeadlines(formItems(new))
	for _, key := range unionOrder(oldOrder, newOrder) {
		ov, inOld := oldItems[key]
		nv, inNew := newItems[key]
		if !inOld {
			addChange(changes, "stages",
				sprintf("The stage %s gains the deadline line %s: %s.", enStage, deadlineLineName(nv), textWord("en", deadlineWords(nv, "en"))),
				sprintf("ステップ%sに期限の行%sが加わります。%s", jaStage, deadlineLineName(nv), textWord("ja", deadlineWords(nv, "ja"))), false)
			continue
		}
		if !inNew {
			addChange(changes, "stages",
				sprintf("The stage %s loses the deadline line %s: %s.", enStage, deadlineLineName(ov), textWord("en", deadlineWords(ov, "en"))),
				sprintf("ステップ%sの期限の行%sがなくなります。%s", jaStage, deadlineLineName(ov), textWord("ja", deadlineWords(ov, "ja"))), false)
			continue
		}
		compareDeadlineLine(enStage, jaStage, ov, nv, changes)
	}
}

func deadlineBehaviorWords(v *jsonValue, lang string) string {
	kind := ""
	if v != nil && v.kind == jsonObject {
		if k, ok := v.get("kind"); ok {
			kind = k.strOr("")
		}
	}
	if lang == "ja" {
		switch kind {
		case "late":
			return "期限を過ぎたときの動作"
		case "empty":
			return "答える人がいないときの動作"
		}
		return "書かれた動作"
	}
	switch kind {
	case "late":
		return "what it does when late"
	case "empty":
		return "what it does when nobody can answer"
	}
	return "the written behavior"
}

func compareDeadlineLine(enStage, jaStage string, old, new *jsonValue, changes *[]change) {
	enLine, jaLine := deadlineLineName(new), deadlineLineName(new)
	machineChanged := false
	for _, key := range []string{"days", "calendar", "written"} {
		ov, _ := old.get(key)
		nv, _ := new.get(key)
		if sameJSON(ov, nv) {
			continue
		}
		machineChanged = true
		switch key {
		case "days":
			addChange(changes, "stages",
				sprintf("At the stage %s, the number of days of the deadline line %s changes from %s to %s. The description said %s and now says %s.", enStage, enLine, valueWord("en", ov), valueWord("en", nv), textWord("en", deadlineWords(old, "en")), textWord("en", deadlineWords(new, "en"))),
				sprintf("ステップ%sでは、期限の行%sの日数が%sから%sに変わります。説明は%sから%sに変わります。", jaStage, jaLine, valueWord("ja", ov), valueWord("ja", nv), textWord("ja", deadlineWords(old, "ja")), textWord("ja", deadlineWords(new, "ja"))), false)
		case "calendar":
			addChange(changes, "stages",
				sprintf("At the stage %s, the calendar of the deadline line %s changes from %s to %s. The description said %s and now says %s.", enStage, enLine, machineWord("en", ov), machineWord("en", nv), textWord("en", deadlineWords(old, "en")), textWord("en", deadlineWords(new, "en"))),
				sprintf("ステップ%sでは、期限の行%sのカレンダーが%sから%sに変わります。説明は%sから%sに変わります。", jaStage, jaLine, machineWord("ja", ov), machineWord("ja", nv), textWord("ja", deadlineWords(old, "ja")), textWord("ja", deadlineWords(new, "ja"))), false)
		case "written":
			enWhat := deadlineBehaviorWords(new, "en")
			jaWhat := deadlineBehaviorWords(new, "ja")
			addChange(changes, "stages",
				sprintf("At the stage %s, the deadline line %s changes %s from %s to %s. The description said %s and now says %s.", enStage, enLine, enWhat, machineWord("en", ov), machineWord("en", nv), textWord("en", deadlineWords(old, "en")), textWord("en", deadlineWords(new, "en"))),
				sprintf("ステップ%sでは、期限の行%sの%sが%sから%sに変わります。説明は%sから%sに変わります。", jaStage, jaLine, jaWhat, machineWord("ja", ov), machineWord("ja", nv), textWord("ja", deadlineWords(old, "ja")), textWord("ja", deadlineWords(new, "ja"))), false)
		}
	}
	if machineChanged {
		return
	}
	for _, lang := range []string{"en", "ja"} {
		ov, _ := old.get(lang)
		nv, _ := new.get(lang)
		if sameJSON(ov, nv) {
			continue
		}
		if lang == "en" {
			addChange(changes, "stages",
				sprintf("At the stage %s, the English sentence of the deadline line %s changes from %s to %s.", enStage, enLine, valueWord("en", ov), valueWord("en", nv)),
				sprintf("ステップ%sでは、期限の行%sの英語の文が%sから%sに変わります。", jaStage, jaLine, valueWord("ja", ov), valueWord("ja", nv)), false)
		} else {
			addChange(changes, "stages",
				sprintf("At the stage %s, the Japanese sentence of the deadline line %s changes from %s to %s.", enStage, enLine, valueWord("en", ov), valueWord("en", nv)),
				sprintf("ステップ%sでは、期限の行%sの日本語の文が%sから%sに変わります。", jaStage, jaLine, valueWord("ja", ov), valueWord("ja", nv)), false)
		}
	}
}

func compareWayHow(enWay, jaWay string, old, new *jsonValue, changes *[]change) {
	if sameJSON(old, new) {
		return
	}
	if old == nil || old.kind == jsonNull {
		addChange(changes, "approvers",
			sprintf("The way to choose approvers %s now says whom it chooses: %s.", enWay, textWord("en", bilingualWord(new, "en"))),
			sprintf("承認者の決め方%sが選ぶ人として%sと書かれるようになります。", jaWay, textWord("ja", bilingualWord(new, "ja"))), false)
		return
	}
	if new == nil || new.kind == jsonNull {
		addChange(changes, "approvers",
			sprintf("The way to choose approvers %s no longer says whom it chooses: it said %s.", enWay, textWord("en", bilingualWord(old, "en"))),
			sprintf("承認者の決め方%sが選ぶ人の文がなくなります。これまでは%sでした。", jaWay, textWord("ja", bilingualWord(old, "ja"))), false)
		return
	}
	addChange(changes, "approvers",
		sprintf("The way to choose approvers %s changes whom it chooses from %s to %s.", enWay, textWord("en", bilingualWord(old, "en")), textWord("en", bilingualWord(new, "en"))),
		sprintf("承認者の決め方%sは、選ぶ人が%sから%sに変わります。", jaWay, textWord("ja", bilingualWord(old, "ja")), textWord("ja", bilingualWord(new, "ja"))), false)
}

func compareLateTables(old, new *jsonValue, changes *[]change) {
	if (old != nil && old.kind != jsonArray) || (new != nil && new.kind != jsonArray) {
		compareOtherKey("late_tables", old, new, changes)
		return
	}
	oldArr, newArr := formItems(old), formItems(new)
	oldOrder, oldItems, _ := keyedOrder(oldArr)
	newOrder, newItems, _ := keyedOrder(newArr)
	for _, name := range unionOrder(oldOrder, newOrder) {
		ov, inOld := oldItems[name]
		nv, inNew := newItems[name]
		if !inOld {
			addChange(changes, "stages", sprintf("The late table `%s` is added.", name), sprintf("期限を過ぎたときに上へ回る表`%s`が追加されます。", name), false)
			continue
		}
		if !inNew {
			addChange(changes, "stages", sprintf("The late table `%s` is removed.", name), sprintf("期限を過ぎたときに上へ回る表`%s`がなくなります。", name), false)
			continue
		}
		compareLateTable(name, ov, nv, changes)
	}
}

func tablePart(table *jsonValue, lang, part string) *jsonValue {
	lv, ok := table.get(lang)
	if !ok || lv.kind != jsonObject {
		return nil
	}
	v, _ := lv.get(part)
	return v
}

func tablePartLen(table *jsonValue, part string) int {
	n := 0
	for _, lang := range []string{"en", "ja"} {
		v := tablePart(table, lang, part)
		if v != nil && v.kind == jsonArray && len(v.arr) > n {
			n = len(v.arr)
		}
	}
	return n
}

func tableItemAt(table *jsonValue, lang, part string, index int) *jsonValue {
	v := tablePart(table, lang, part)
	if v == nil || v.kind != jsonArray || index >= len(v.arr) {
		return nil
	}
	return v.arr[index]
}

func tableItem(table *jsonValue, lang, part string, index int) *jsonValue {
	v := tableItemAt(table, lang, part, index)
	if v != nil {
		return v
	}
	other := "en"
	if lang == "en" {
		other = "ja"
	}
	return tableItemAt(table, other, part, index)
}

func tableCellWord(v *jsonValue) string {
	if v == nil {
		return ""
	}
	if v.kind == jsonString {
		return v.str
	}
	return v.compact()
}

func tableRowWord(v *jsonValue) string {
	if v == nil {
		return ""
	}
	if v.kind != jsonArray {
		return shortJSON(v)
	}
	cells := make([]string, 0, len(v.arr))
	for _, cell := range v.arr {
		cells = append(cells, tableCellWord(cell))
	}
	return strings.Join(cells, " | ")
}

func tableItemWord(table *jsonValue, lang, part string, index int) string {
	v := tableItem(table, lang, part, index)
	if part == "rows" {
		return tableRowWord(v)
	}
	return tableCellWord(v)
}

func tableItemExists(table *jsonValue, part string, index int) bool {
	return tableItemAt(table, "en", part, index) != nil || tableItemAt(table, "ja", part, index) != nil
}

func compareLateTable(name string, old, new *jsonValue, changes *[]change) {
	compareLateTablePart(name, old, new, "head", changes)
	compareLateTablePart(name, old, new, "rows", changes)
}

type tableLogicalItem struct {
	index int
	key   string
}

func tableLogicalItems(table *jsonValue, part string) []tableLogicalItem {
	n := tablePartLen(table, part)
	items := make([]tableLogicalItem, 0, n)
	for i := 0; i < n; i++ {
		en := tableItemAt(table, "en", part, i)
		ja := tableItemAt(table, "ja", part, i)
		if en == nil && ja == nil {
			continue
		}
		items = append(items, tableLogicalItem{index: i, key: canonicalJSON(en) + "\x00" + canonicalJSON(ja)})
	}
	return items
}

type tableItemEdit struct {
	old, new int
}

// alignTableItems matches rows or head cells that are written unchanged before it reads the
// entries left over as edits. Without that first step, removing one row would make every row after
// it look changed only because it moved one position up.
func alignTableItems(old, new []tableLogicalItem) (edits []tableItemEdit, removed, added []int, orderChanged bool) {
	newByKey := map[string][]int{}
	for i, x := range new {
		newByKey[x.key] = append(newByKey[x.key], i)
	}
	exactNewOfOld := map[int]int{}
	usedNew := make([]bool, len(new))
	var exactPositions []int
	for oi, x := range old {
		queue := newByKey[x.key]
		if len(queue) == 0 {
			continue
		}
		ni := queue[0]
		newByKey[x.key] = queue[1:]
		exactNewOfOld[oi] = ni
		usedNew[ni] = true
		exactPositions = append(exactPositions, ni)
	}
	for i := 1; i < len(exactPositions); i++ {
		if exactPositions[i] < exactPositions[i-1] {
			orderChanged = true
			break
		}
	}
	var oldLeft, newLeft []int
	for i := range old {
		if _, ok := exactNewOfOld[i]; !ok {
			oldLeft = append(oldLeft, i)
		}
	}
	for i := range new {
		if !usedNew[i] {
			newLeft = append(newLeft, i)
		}
	}
	n := min(len(oldLeft), len(newLeft))
	for i := 0; i < n; i++ {
		edits = append(edits, tableItemEdit{old: oldLeft[i], new: newLeft[i]})
	}
	for _, i := range oldLeft[n:] {
		removed = append(removed, i)
	}
	for _, i := range newLeft[n:] {
		added = append(added, i)
	}
	return edits, removed, added, orderChanged
}

func compareLateTablePart(name string, old, new *jsonValue, part string, changes *[]change) {
	oldItems, newItems := tableLogicalItems(old, part), tableLogicalItems(new, part)
	edits, removed, added, orderChanged := alignTableItems(oldItems, newItems)
	if orderChanged {
		if part == "head" {
			addChange(changes, "stages",
				sprintf("The order of the head of the late table `%s` changes.", name),
				sprintf("期限を過ぎたときに上へ回る表`%s`の見出しの順が変わります。", name), false)
		} else {
			addChange(changes, "stages",
				sprintf("The order of the rows of the late table `%s` changes.", name),
				sprintf("期限を過ぎたときに上へ回る表`%s`の行の順が変わります。", name), false)
		}
	}
	for _, edit := range edits {
		oi, ni := oldItems[edit.old].index, newItems[edit.new].index
		enOld, jaOld := tableItemWord(old, "en", part, oi), tableItemWord(old, "ja", part, oi)
		enNew, jaNew := tableItemWord(new, "en", part, ni), tableItemWord(new, "ja", part, ni)
		if part == "head" {
			addChange(changes, "stages",
				sprintf("The head of column %d of the late table `%s` changes from %s to %s.", oi+1, name, textWord("en", enOld), textWord("en", enNew)),
				sprintf("期限を過ぎたときに上へ回る表`%s`の%d列目の見出しが%sから%sに変わります。", name, oi+1, textWord("ja", jaOld), textWord("ja", jaNew)), false)
		} else {
			addChange(changes, "stages",
				sprintf("Row %d of the late table `%s` changes from %s to %s.", oi+1, name, textWord("en", enOld), textWord("en", enNew)),
				sprintf("期限を過ぎたときに上へ回る表`%s`の%d行目が%sから%sに変わります。", name, oi+1, textWord("ja", jaOld), textWord("ja", jaNew)), false)
		}
	}
	for _, oldItem := range removed {
		i := oldItems[oldItem].index
		enOld, jaOld := tableItemWord(old, "en", part, i), tableItemWord(old, "ja", part, i)
		if part == "head" {
			addChange(changes, "stages",
				sprintf("The late table `%s` loses the head of column %d: %s.", name, i+1, textWord("en", enOld)),
				sprintf("期限を過ぎたときに上へ回る表`%s`の%d列目の見出し%sがなくなります。", name, i+1, textWord("ja", jaOld)), false)
		} else {
			addChange(changes, "stages",
				sprintf("The late table `%s` loses row %d: %s.", name, i+1, textWord("en", enOld)),
				sprintf("期限を過ぎたときに上へ回る表`%s`の%d行目%sがなくなります。", name, i+1, textWord("ja", jaOld)), false)
		}
	}
	for _, newItem := range added {
		i := newItems[newItem].index
		enNew, jaNew := tableItemWord(new, "en", part, i), tableItemWord(new, "ja", part, i)
		if part == "head" {
			addChange(changes, "stages",
				sprintf("The late table `%s` gains the head of column %d: %s.", name, i+1, textWord("en", enNew)),
				sprintf("期限を過ぎたときに上へ回る表`%s`に%d列目の見出し%sが加わります。", name, i+1, textWord("ja", jaNew)), false)
		} else {
			addChange(changes, "stages",
				sprintf("The late table `%s` gains row %d: %s.", name, i+1, textWord("en", enNew)),
				sprintf("期限を過ぎたときに上へ回る表`%s`に%d行目として%sが加わります。", name, i+1, textWord("ja", jaNew)), false)
		}
	}
}

package learn

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AGlElectronics/learn/backend/internal/db"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
)

// Translatable entity types (translations.entity_type).
const (
	TrCourse        = "course"
	TrSection       = "section"
	TrActivity      = "activity"
	TrQuestion      = "question"
	TrOption        = "option"
	TrMedia         = "media"
	TrActivityMedia = "activity_media"
	TrPath          = "path"
	TrQualification = "qualification"
)

// Translation scopes: the staff editor works on one course, qualification
// or path at a time.
const (
	TrScopeCourse        = "course"
	TrScopeQualification = "qualification"
	TrScopePath          = "path"
)

const (
	maxCourseLanguages   = 20
	maxTranslationsPerPut = 5000
)

var langCodeRE = regexp.MustCompile(`^[a-z]{2,3}$`)

// NormalizeLang reduces a language tag to its primary subtag ("nl-BE" →
// "nl"); "" when it is not a language code.
func NormalizeLang(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if i := strings.IndexAny(s, "-_"); i >= 0 {
		s = s[:i]
	}
	if !langCodeRE.MatchString(s) {
		return ""
	}
	return s
}

// Langs is what a caller would like to read, best first.
type Langs []string

// ParseAcceptLanguage returns the languages of an Accept-Language header,
// highest q first (ties keep header order), without q=0 entries.
func ParseAcceptLanguage(header string) []string {
	type entry struct {
		lang string
		q    float64
		pos  int
	}
	var entries []entry
	for i, part := range strings.Split(header, ",") {
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		lang := NormalizeLang(tag)
		if lang == "" {
			continue
		}
		q := 1.0
		for _, p := range strings.Split(params, ";") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(p), "q="); ok {
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					q = f
				}
			}
		}
		if q > 0 {
			entries = append(entries, entry{lang, q, i})
		}
	}
	sort.SliceStable(entries, func(a, b int) bool { return entries[a].q > entries[b].q })
	out := []string{}
	for _, e := range entries {
		out = append(out, e.lang)
	}
	return out
}

// NewLangs orders a request's wishes: the ?lang= override, then the saved
// preference, then Accept-Language. Invalid and repeated codes are dropped.
func NewLangs(override, preference, acceptLanguage string) Langs {
	seen := map[string]bool{}
	out := Langs{}
	add := func(raw string) {
		if l := NormalizeLang(raw); l != "" && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	add(override)
	add(preference)
	for _, l := range ParseAcceptLanguage(acceptLanguage) {
		add(l)
	}
	return out
}

// Pick returns the first wish among offered, else def.
func (l Langs) Pick(offered []string, def string) string {
	for _, want := range l {
		for _, have := range offered {
			if want == have {
				return want
			}
		}
	}
	return def
}

// LangFor is the language a course is served in for these wishes.
func (c Course) LangFor(l Langs) string {
	return l.Pick(c.Languages, c.DefaultLang)
}

// ----------------------------------------------------------------------------
// Applying translations to payloads
// ----------------------------------------------------------------------------

// trItem is one string in a payload that may be replaced by a translation.
// course is the course whose language applies; nil means the entity has no
// language list of its own (paths, qualifications), and then only the
// caller's first wish is used, falling back to the base text.
type trItem struct {
	course *uuid.UUID
	typ    string
	id     uuid.UUID
	field  string
	dst    *string
}

type translator struct {
	s     *Service
	langs Langs
	items []trItem
}

func (s *Service) translator(langs Langs) *translator {
	return &translator{s: s, langs: langs}
}

func (t *translator) course(courseID uuid.UUID, typ string, id uuid.UUID, field string, dst *string) {
	cid := courseID
	t.items = append(t.items, trItem{course: &cid, typ: typ, id: id, field: field, dst: dst})
}

func (t *translator) loose(typ string, id uuid.UUID, field string, dst *string) {
	t.items = append(t.items, trItem{typ: typ, id: id, field: field, dst: dst})
}

func (t *translator) media(courseID uuid.UUID, m *MediaRef) {
	if m != nil {
		t.course(courseID, TrMedia, m.ID, "alt_text", &m.AltText)
	}
}

// apply resolves each course's language (skipping courses served in their
// default language), loads the matching translations in one query and
// overwrites the targets that have a non-empty translation.
func (t *translator) apply(ctx context.Context) error {
	if len(t.langs) == 0 || len(t.items) == 0 {
		return nil
	}
	courseIDs := []uuid.UUID{}
	seenCourse := map[uuid.UUID]bool{}
	for _, it := range t.items {
		if it.course != nil && !seenCourse[*it.course] {
			seenCourse[*it.course] = true
			courseIDs = append(courseIDs, *it.course)
		}
	}
	langOf := map[uuid.UUID]string{}
	if len(courseIDs) > 0 {
		rows, err := t.s.pool.Query(ctx, `SELECT id, default_lang, languages FROM courses WHERE id = ANY($1)`, courseIDs)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id uuid.UUID
			var def string
			var offered []string
			if err := rows.Scan(&id, &def, &offered); err != nil {
				rows.Close()
				return err
			}
			if l := t.langs.Pick(offered, def); l != def {
				langOf[id] = l
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}

	type want struct {
		trItem
		lang string
	}
	wants := []want{}
	ids := []uuid.UUID{}
	langs := []string{}
	seenID := map[uuid.UUID]bool{}
	seenLang := map[string]bool{}
	for _, it := range t.items {
		lang := t.langs[0]
		if it.course != nil {
			lang = langOf[*it.course]
		}
		if lang == "" {
			continue
		}
		wants = append(wants, want{it, lang})
		if !seenID[it.id] {
			seenID[it.id] = true
			ids = append(ids, it.id)
		}
		if !seenLang[lang] {
			seenLang[lang] = true
			langs = append(langs, lang)
		}
	}
	if len(wants) == 0 {
		return nil
	}
	type key struct {
		typ   string
		id    uuid.UUID
		field string
		lang  string
	}
	found := map[key]string{}
	rows, err := t.s.pool.Query(ctx, `
		SELECT entity_type, entity_id, field, lang, text FROM translations
		 WHERE entity_id = ANY($1) AND lang = ANY($2)`, ids, langs)
	if err != nil {
		return err
	}
	for rows.Next() {
		var k key
		var text string
		if err := rows.Scan(&k.typ, &k.id, &k.field, &k.lang, &text); err != nil {
			rows.Close()
			return err
		}
		found[k] = text
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, w := range wants {
		if text, ok := found[key{w.typ, w.id, w.field, w.lang}]; ok && strings.TrimSpace(text) != "" {
			*w.dst = text
		}
	}
	return nil
}

// TranslateDetail serves a course detail in the course's language for
// langs (nil = the default text, as staff edit it). Prerequisite names in
// Needs follow the translated activity titles.
func (s *Service) TranslateDetail(ctx context.Context, d *CourseDetail, langs Langs) error {
	if langs == nil {
		return nil
	}
	d.Lang = d.Course.LangFor(langs)
	t := s.translator(langs)
	cid := d.ID
	t.course(cid, TrCourse, cid, "title", &d.Title)
	t.course(cid, TrCourse, cid, "summary", &d.Summary)
	before := map[uuid.UUID]string{}
	for i := range d.Sections {
		sec := &d.Sections[i]
		t.course(cid, TrSection, sec.ID, "title", &sec.Title)
		for j := range sec.Activities {
			a := &sec.Activities[j]
			before[a.ID] = a.Title
			t.course(cid, TrActivity, a.ID, "title", &a.Title)
			if !a.Locked {
				t.course(cid, TrActivity, a.ID, "body", &a.Body)
			}
		}
	}
	if d.AwardsQualification != nil {
		t.loose(TrQualification, d.AwardsQualification.ID, "name", &d.AwardsQualification.Name)
	}
	if err := t.apply(ctx); err != nil {
		return err
	}
	rename := map[string]string{}
	for i := range d.Sections {
		for _, a := range d.Sections[i].Activities {
			if old := before[a.ID]; old != a.Title {
				rename[old] = a.Title
			}
		}
	}
	for i := range d.Sections {
		for j := range d.Sections[i].Activities {
			needs := d.Sections[i].Activities[j].Needs
			for k, n := range needs {
				if r, ok := rename[n]; ok {
					needs[k] = r
				}
			}
		}
	}
	return nil
}

// TranslateSummaries serves course list entries in each course's language.
func (s *Service) TranslateSummaries(ctx context.Context, list []CourseSummary, langs Langs) error {
	if langs == nil {
		return nil
	}
	t := s.translator(langs)
	for i := range list {
		c := &list[i]
		c.Lang = c.Course.LangFor(langs)
		t.course(c.ID, TrCourse, c.ID, "title", &c.Title)
		t.course(c.ID, TrCourse, c.ID, "summary", &c.Summary)
	}
	return t.apply(ctx)
}

// TranslateQuizInfo translates a quiz's title and intro.
func (s *Service) TranslateQuizInfo(ctx context.Context, courseID uuid.UUID, info *QuizInfo, langs Langs) error {
	t := s.translator(langs)
	t.course(courseID, TrActivity, info.ActivityID, "title", &info.Title)
	t.course(courseID, TrActivity, info.ActivityID, "body", &info.Body)
	return t.apply(ctx)
}

// TranslateAttempt translates an attempt's prompts, feedback, option labels
// and alt texts. Ids are untouched, so marking is unaffected.
func (s *Service) TranslateAttempt(ctx context.Context, courseID uuid.UUID, a *Attempt, langs Langs) error {
	t := s.translator(langs)
	for i := range a.Questions {
		q := &a.Questions[i]
		t.course(courseID, TrQuestion, q.QuestionID, "prompt", &q.Prompt)
		if q.Feedback != nil {
			t.course(courseID, TrQuestion, q.QuestionID, "feedback", q.Feedback)
		}
		t.media(courseID, q.Media)
		t.media(courseID, q.FeedbackMedia)
		for j := range q.Options {
			o := &q.Options[j]
			t.course(courseID, TrOption, o.ID, "label", &o.Label)
			t.media(courseID, o.Media)
		}
	}
	return t.apply(ctx)
}

// TranslateActivityMedia translates page attachment captions and alt texts
// (nil langs = staff editing: unchanged).
func (s *Service) TranslateActivityMedia(ctx context.Context, courseID uuid.UUID, list []ActivityMedia, langs Langs) error {
	if langs == nil {
		return nil
	}
	t := s.translator(langs)
	for i := range list {
		t.course(courseID, TrActivityMedia, list[i].ID, "caption", &list[i].Caption)
		t.media(courseID, list[i].Media)
	}
	return t.apply(ctx)
}

// TranslateSessionInfo translates a session's title and description.
func (s *Service) TranslateSessionInfo(ctx context.Context, courseID uuid.UUID, info *SessionInfo, langs Langs) error {
	t := s.translator(langs)
	t.course(courseID, TrActivity, info.ActivityID, "title", &info.Title)
	t.course(courseID, TrActivity, info.ActivityID, "body", &info.Body)
	return t.apply(ctx)
}

// TranslateCheckin translates the session name on the check-in result.
func (s *Service) TranslateCheckin(ctx context.Context, res *CheckinResult, langs Langs) error {
	t := s.translator(langs)
	t.course(res.CourseID, TrActivity, res.Signup.ActivityID, "title", &res.ActivityTitle)
	return t.apply(ctx)
}

// TranslateCertificates translates qualification names and descriptions
// and the awarding course's title.
func (s *Service) TranslateCertificates(ctx context.Context, langs Langs, certs ...*Certificate) error {
	t := s.translator(langs)
	for _, c := range certs {
		t.loose(TrQualification, c.QualificationID, "name", &c.QualificationName)
		t.loose(TrQualification, c.QualificationID, "description", &c.QualificationDescription)
		if c.CourseID != nil {
			t.course(*c.CourseID, TrCourse, *c.CourseID, "title", &c.CourseTitle)
		}
	}
	return t.apply(ctx)
}

// TranslateVerification translates the qualification on the public page.
func (s *Service) TranslateVerification(ctx context.Context, v *PublicVerification, langs Langs) error {
	t := s.translator(langs)
	t.loose(TrQualification, v.QualificationID, "name", &v.Qualification)
	return t.apply(ctx)
}

// TranslateMyPaths translates path names and descriptions, their course
// titles and awarded qualifications.
func (s *Service) TranslateMyPaths(ctx context.Context, mine []MyPath, available []Path, langs Langs) error {
	t := s.translator(langs)
	qual := func(q *QualificationRef) {
		if q != nil {
			t.loose(TrQualification, q.ID, "name", &q.Name)
		}
	}
	for i := range mine {
		p := &mine[i]
		t.loose(TrPath, p.ID, "name", &p.Name)
		t.loose(TrPath, p.ID, "description", &p.Description)
		qual(p.AwardsQualification)
		for j := range p.Courses {
			c := &p.Courses[j]
			t.course(c.CourseID, TrCourse, c.CourseID, "title", &c.Title)
		}
	}
	for i := range available {
		p := &available[i]
		t.loose(TrPath, p.ID, "name", &p.Name)
		t.loose(TrPath, p.ID, "description", &p.Description)
		qual(p.AwardsQualification)
		for j := range p.Courses {
			c := &p.Courses[j]
			t.course(c.CourseID, TrCourse, c.CourseID, "title", &c.Title)
		}
	}
	return t.apply(ctx)
}

// TranslateTeamBoards translates board titles (course or qualification).
func (s *Service) TranslateTeamBoards(ctx context.Context, langs Langs, boards ...*TeamBoard) error {
	t := s.translator(langs)
	for _, b := range boards {
		if b.TargetType == TeamBoardCourse {
			t.course(b.TargetID, TrCourse, b.TargetID, "title", &b.Title)
		} else {
			t.loose(TrQualification, b.TargetID, "name", &b.Title)
		}
	}
	return t.apply(ctx)
}

// ----------------------------------------------------------------------------
// Staff translation editor
// ----------------------------------------------------------------------------

// TranslationField is one translatable string: where it is, the default
// text, the translation in the sheet's language ("" = none) and whether it
// still needs one.
type TranslationField struct {
	EntityType string    `json:"entity_type"`
	EntityID   uuid.UUID `json:"entity_id"`
	Field      string    `json:"field"`
	Context    string    `json:"context"`
	Default    string    `json:"default"`
	Text       string    `json:"text"`
	Missing    bool      `json:"missing"`
}

// TranslationSheet is every translatable field of one course, qualification
// or path in one language.
type TranslationSheet struct {
	Lang         string             `json:"lang"`
	DefaultLang  string             `json:"default_lang,omitempty"`
	Languages    []string           `json:"languages,omitempty"`
	Fields       []TranslationField `json:"fields"`
	MissingCount int                `json:"missing_count"`
}

// TranslationInput is one translation to save; empty Text removes it.
type TranslationInput struct {
	EntityType string    `json:"entity_type"`
	EntityID   uuid.UUID `json:"entity_id"`
	Field      string    `json:"field"`
	Text       string    `json:"text"`
}

// trSource lists one (entity type, field) of a scope: rows of (id,
// context, default text) for scope id $1, in display order.
type trSource struct {
	typ, field, sql string
}

const trActivityOrder = ` ORDER BY s.position, s.created_at, a.position, a.created_at`
const trQuestionOrder = ` ORDER BY qc.position, qc.created_at, q.position, q.created_at`

var trSources = map[string][]trSource{
	TrScopeCourse: {
		{TrCourse, "title", `SELECT id, '', title FROM courses WHERE id = $1`},
		{TrCourse, "summary", `SELECT id, '', summary FROM courses WHERE id = $1`},
		{TrSection, "title", `SELECT id, '', title FROM sections WHERE course_id = $1 ORDER BY position, created_at`},
		{TrActivity, "title", `SELECT a.id, s.title, a.title FROM activities a JOIN sections s ON s.id = a.section_id
			WHERE s.course_id = $1` + trActivityOrder},
		{TrActivity, "body", `SELECT a.id, s.title || ' › ' || a.title, a.body FROM activities a JOIN sections s ON s.id = a.section_id
			WHERE s.course_id = $1` + trActivityOrder},
		{TrActivityMedia, "caption", `SELECT am.id, a.title, am.caption FROM activity_media am
			JOIN activities a ON a.id = am.activity_id JOIN sections s ON s.id = a.section_id
			WHERE s.course_id = $1` + trActivityOrder + `, am.position`},
		{TrQuestion, "prompt", `SELECT q.id, qc.name, q.prompt FROM questions q JOIN question_categories qc ON qc.id = q.category_id
			WHERE qc.course_id = $1` + trQuestionOrder},
		{TrQuestion, "feedback", `SELECT q.id, qc.name || ' › ' || q.prompt, q.feedback FROM questions q
			JOIN question_categories qc ON qc.id = q.category_id
			WHERE qc.course_id = $1` + trQuestionOrder},
		{TrOption, "label", `SELECT o.id, q.prompt, o.label FROM question_options o
			JOIN questions q ON q.id = o.question_id JOIN question_categories qc ON qc.id = q.category_id
			WHERE qc.course_id = $1` + trQuestionOrder + `, o.position`},
		{TrMedia, "alt_text", `SELECT id, name, alt_text FROM media WHERE course_id = $1 ORDER BY created_at, id`},
	},
	TrScopeQualification: {
		{TrQualification, "name", `SELECT id, '', name FROM qualifications WHERE id = $1`},
		{TrQualification, "description", `SELECT id, '', description FROM qualifications WHERE id = $1`},
	},
	TrScopePath: {
		{TrPath, "name", `SELECT id, '', name FROM paths WHERE id = $1`},
		{TrPath, "description", `SELECT id, '', description FROM paths WHERE id = $1`},
	},
}

// translationFields lists every field of a scope (including empty ones)
// with its translation in lang.
func (s *Service) translationFields(ctx context.Context, q dbtx, scope string, id uuid.UUID, lang string) ([]TranslationField, error) {
	sources, ok := trSources[scope]
	if !ok {
		return nil, httperror.BadRequest("unknown translation scope")
	}
	out := []TranslationField{}
	for _, src := range sources {
		rows, err := q.Query(ctx, src.sql, id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			f := TranslationField{EntityType: src.typ, Field: src.field}
			if err := rows.Scan(&f.EntityID, &f.Context, &f.Default); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, f)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	if lang == "" || len(out) == 0 {
		return out, nil
	}
	ids := make([]uuid.UUID, 0, len(out))
	for _, f := range out {
		ids = append(ids, f.EntityID)
	}
	type key struct {
		typ   string
		id    uuid.UUID
		field string
	}
	texts := map[key]string{}
	rows, err := q.Query(ctx, `
		SELECT entity_type, entity_id, field, text FROM translations
		 WHERE entity_id = ANY($1) AND lang = $2`, ids, lang)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k key
		var text string
		if err := rows.Scan(&k.typ, &k.id, &k.field, &text); err != nil {
			rows.Close()
			return nil, err
		}
		texts[k] = text
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Text = texts[key{out[i].EntityType, out[i].EntityID, out[i].Field}]
	}
	return out, nil
}

// TranslationSheet returns the non-empty translatable fields of a scope in
// lang, flagging the ones without a translation. For a course without lang
// the first non-default language is used ("" when it has none).
func (s *Service) TranslationSheet(ctx context.Context, scope string, id uuid.UUID, lang string) (TranslationSheet, error) {
	sheet := TranslationSheet{Fields: []TranslationField{}}
	if scope == TrScopeCourse {
		c, err := s.GetCourse(ctx, id)
		if err != nil {
			return sheet, err
		}
		sheet.DefaultLang, sheet.Languages = c.DefaultLang, c.Languages
		if lang == "" {
			for _, l := range c.Languages {
				if l != c.DefaultLang {
					lang = l
					break
				}
			}
		}
		if lang == "" {
			return sheet, nil
		}
	}
	if err := checkSheetLang(sheet, lang); err != nil {
		return sheet, err
	}
	sheet.Lang = lang
	all, err := s.translationFields(ctx, s.pool, scope, id, lang)
	if err != nil {
		return sheet, err
	}
	for _, f := range all {
		if strings.TrimSpace(f.Default) == "" {
			continue
		}
		f.Missing = strings.TrimSpace(f.Text) == ""
		if f.Missing {
			sheet.MissingCount++
		}
		sheet.Fields = append(sheet.Fields, f)
	}
	return sheet, nil
}

// checkSheetLang: a valid code and, for courses, one of its languages other
// than the default (whose text lives in the base columns).
func checkSheetLang(sheet TranslationSheet, lang string) error {
	if NormalizeLang(lang) != lang {
		return httperror.Validation("lang must be a language code such as nl", nil)
	}
	if sheet.DefaultLang == "" {
		return nil
	}
	if lang == sheet.DefaultLang {
		return httperror.Validation("the default language is edited in the course itself, not as a translation", nil)
	}
	for _, l := range sheet.Languages {
		if l == lang {
			return nil
		}
	}
	return httperror.Validation("add "+lang+" to the course languages first", nil)
}

// PutTranslations upserts translations of one scope in lang (empty text
// deletes) and returns the updated sheet. Every item must name a field of
// that scope.
func (s *Service) PutTranslations(ctx context.Context, scope string, id uuid.UUID, lang string, items []TranslationInput) (TranslationSheet, error) {
	if len(items) > maxTranslationsPerPut {
		return TranslationSheet{}, httperror.Validation("too many translations in one request", nil)
	}
	sheet := TranslationSheet{}
	if scope == TrScopeCourse {
		c, err := s.GetCourse(ctx, id)
		if err != nil {
			return sheet, err
		}
		sheet.DefaultLang, sheet.Languages = c.DefaultLang, c.Languages
	}
	if err := checkSheetLang(sheet, lang); err != nil {
		return sheet, err
	}
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		all, err := s.translationFields(ctx, tx, scope, id, "")
		if err != nil {
			return err
		}
		type key struct {
			typ   string
			id    uuid.UUID
			field string
		}
		allowed := map[key]bool{}
		for _, f := range all {
			allowed[key{f.EntityType, f.EntityID, f.Field}] = true
		}
		for _, it := range items {
			if !allowed[key{it.EntityType, it.EntityID, it.Field}] {
				return httperror.Validation("a translation refers to a field that is not part of this "+scope, map[string]any{
					"entity_type": it.EntityType, "entity_id": it.EntityID, "field": it.Field,
				})
			}
			text := strings.TrimSpace(it.Text)
			if text == "" {
				if _, err := tx.Exec(ctx, `
					DELETE FROM translations WHERE entity_type = $1 AND entity_id = $2 AND field = $3 AND lang = $4`,
					it.EntityType, it.EntityID, it.Field, lang); err != nil {
					return err
				}
				continue
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO translations (entity_type, entity_id, field, lang, text)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (entity_type, entity_id, field, lang)
				DO UPDATE SET text = EXCLUDED.text, updated_at = now()`,
				it.EntityType, it.EntityID, it.Field, lang, text); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return TranslationSheet{}, err
	}
	return s.TranslationSheet(ctx, scope, id, lang)
}

// SetCourseLanguages changes the offered languages and/or the default
// (nil = unchanged). The default is always offered; translations of a
// removed language are kept in case it comes back.
func (s *Service) SetCourseLanguages(ctx context.Context, courseID uuid.UUID, languages *[]string, defaultLang *string) error {
	if languages == nil && defaultLang == nil {
		return nil
	}
	c, err := s.GetCourse(ctx, courseID)
	if err != nil {
		return err
	}
	def := c.DefaultLang
	if defaultLang != nil {
		if def = NormalizeLang(*defaultLang); def == "" {
			return httperror.Validation("default_lang must be a language code such as en", nil)
		}
	}
	list := c.Languages
	if languages != nil {
		list = []string{}
		seen := map[string]bool{}
		for _, raw := range *languages {
			l := NormalizeLang(raw)
			if l == "" {
				return httperror.Validation("languages must be language codes such as en or nl", map[string]any{"language": raw})
			}
			if !seen[l] {
				seen[l] = true
				list = append(list, l)
			}
		}
	}
	offered := false
	for _, l := range list {
		offered = offered || l == def
	}
	if !offered {
		list = append([]string{def}, list...)
	}
	if len(list) > maxCourseLanguages {
		return httperror.Validation("a course can offer at most 20 languages", nil)
	}
	_, err = s.pool.Exec(ctx, `UPDATE courses SET default_lang = $2, languages = $3, updated_at = now() WHERE id = $1`,
		courseID, def, list)
	return err
}

// ----------------------------------------------------------------------------
// Saved language preference
// ----------------------------------------------------------------------------

// UserLanguage returns the person's saved language ("" = none).
func (s *Service) UserLanguage(ctx context.Context, userID uuid.UUID) (string, error) {
	var lang *string
	err := s.pool.QueryRow(ctx, `SELECT language FROM users WHERE id = $1`, userID).Scan(&lang)
	if errors.Is(err, pgx.ErrNoRows) || lang == nil {
		return "", nil
	}
	return *lang, err
}

// SetUserLanguage saves (or with "" clears) the person's language.
func (s *Service) SetUserLanguage(ctx context.Context, userID uuid.UUID, lang string) error {
	var v *string
	if lang != "" {
		l := NormalizeLang(lang)
		if l == "" {
			return httperror.Validation("language must be a language code such as en or nl", nil)
		}
		v = &l
	}
	_, err := s.pool.Exec(ctx, `UPDATE users SET language = $2 WHERE id = $1`, userID, v)
	return err
}

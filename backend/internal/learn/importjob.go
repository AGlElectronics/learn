package learn

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AGlElectronics/learn/backend/internal/db"
	"github.com/AGlElectronics/learn/backend/internal/domain"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
	"github.com/AGlElectronics/learn/backend/internal/importer"
)

// ObjectPutter is the part of object storage the question import needs to
// store embedded images. *files.Client implements it.
type ObjectPutter interface {
	PutObject(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
}

// ImportFormat maps an API format name to the parser's ("" = detect).
// Moodle XML is accepted as "moodle" or "moodle_xml".
func ImportFormat(name, filename string, data []byte) (importer.Format, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "":
		f := importer.DetectFormat(filename, data)
		if f == "" {
			return "", httperror.Validation("could not tell the file format; choose Moodle XML, GIFT, Aiken or CSV", nil)
		}
		return f, nil
	case "moodle", "moodle_xml", "xml":
		return importer.FormatMoodleXML, nil
	case "gift":
		return importer.FormatGIFT, nil
	case "aiken":
		return importer.FormatAiken, nil
	case "csv":
		return importer.FormatCSV, nil
	}
	return "", httperror.Validation("format must be moodle_xml, gift, aiken or csv", nil)
}

// ImportWarning is one skipped or partly imported item, with its 1-based
// Index.
type ImportWarning struct {
	Index   int    `json:"index"`
	Message string `json:"message"`
}

// ImportOption / ImportQuestion are the preview shape of a parsed question.
type ImportOption struct {
	Label   string `json:"label"`
	Correct bool   `json:"correct"`
}

type ImportQuestion struct {
	Index    int            `json:"index"`
	Kind     string         `json:"kind"`
	Prompt   string         `json:"prompt"`
	Feedback string         `json:"feedback"`
	Points   int            `json:"points"`
	Options  []ImportOption `json:"options"`
	Images   []string       `json:"images"`
}

// ImportPreview is what a file would import, without saving anything.
type ImportPreview struct {
	Format    importer.Format  `json:"format"`
	Questions []ImportQuestion `json:"questions"`
	Warnings  []ImportWarning  `json:"warnings"`
}

// ImportResult reports a commit. Skipped counts parsed questions that
// failed validation here; items the parser dropped are only in Warnings.
type ImportResult struct {
	Format   importer.Format `json:"format"`
	Imported int             `json:"imported"`
	Skipped  int             `json:"skipped"`
	Images   int             `json:"images"`
	Warnings []ImportWarning `json:"warnings"`
}

func parseImport(format importer.Format, data []byte) ([]importer.Question, []ImportWarning, error) {
	qs, ws, err := importer.Parse(format, bytes.NewReader(data))
	if err != nil {
		return nil, nil, httperror.Validation("the file could not be read: "+err.Error(), nil)
	}
	warnings := make([]ImportWarning, 0, len(ws))
	for _, w := range ws {
		warnings = append(warnings, ImportWarning{Index: w.Index, Message: w.Message})
	}
	return qs, warnings, nil
}

// PreviewImport parses a file and reports what would be imported.
func PreviewImport(format importer.Format, data []byte) (ImportPreview, error) {
	qs, warnings, err := parseImport(format, data)
	if err != nil {
		return ImportPreview{}, err
	}
	out := ImportPreview{Format: format, Questions: make([]ImportQuestion, 0, len(qs)), Warnings: warnings}
	for i, q := range qs {
		iq := ImportQuestion{
			Index: i + 1, Kind: q.Kind, Prompt: q.Prompt, Feedback: q.Feedback, Points: q.Points,
			Options: make([]ImportOption, 0, len(q.Options)), Images: []string{},
		}
		for _, o := range q.Options {
			iq.Options = append(iq.Options, ImportOption{Label: o.Label, Correct: o.Correct})
		}
		for _, img := range q.Images {
			iq.Images = append(iq.Images, img.Filename)
		}
		out.Questions = append(out.Questions, iq)
	}
	return out, nil
}

// importedImage is an embedded image already uploaded to storage.
type importedImage struct {
	id            uuid.UUID
	key, ct, name string
	size          int64
}

// CommitImport parses a file and adds its questions to a category in one
// transaction. Questions that fail the bank's validation are skipped with
// a warning. The first embedded image of a question becomes its picture
// (alt text = file name); without object storage images are skipped.
func (s *Service) CommitImport(ctx context.Context, store ObjectPutter, courseID, categoryID, userID uuid.UUID,
	format importer.Format, data []byte) (ImportResult, error) {
	cat, err := s.CategoryCourse(ctx, categoryID)
	if err != nil {
		return ImportResult{}, err
	}
	if cat.ID != courseID {
		return ImportResult{}, httperror.Validation("the category must belong to this course", nil)
	}
	qs, warnings, err := parseImport(format, data)
	if err != nil {
		return ImportResult{}, err
	}
	res := ImportResult{Format: format, Warnings: warnings}

	type pending struct {
		q     importer.Question
		image *importedImage
	}
	var todo []pending
	var uploaded []importedImage
	for i, q := range qs {
		// Parser warnings index source items; these name the question
		// (Index is its row in the preview).
		excerpt := strings.TrimSpace(q.Prompt)
		if r := []rune(excerpt); len(r) > 60 {
			excerpt = string(r[:60]) + "…"
		}
		warn := func(msg string) {
			res.Warnings = append(res.Warnings, ImportWarning{i + 1, fmt.Sprintf("%q: %s", excerpt, msg)})
		}
		kind := q.Kind
		if kind != domain.QuestionSingle && kind != domain.QuestionMultiple {
			warn("skipped: unsupported question type " + kind)
			res.Skipped++
			continue
		}
		draft := Question{Kind: kind}
		opts := q.Options[:0:0]
		for _, o := range q.Options {
			o.Label = strings.TrimSpace(o.Label)
			if o.Label == "" {
				continue
			}
			opts = append(opts, o)
			draft.Options = append(draft.Options, QuestionOption{Label: o.Label, Correct: o.Correct})
		}
		q.Options = opts
		if excerpt == "" {
			warn("skipped: question text is empty")
			res.Skipped++
			continue
		}
		if err := validateQuestion(draft); err != nil {
			msg := err.Error()
			var he *httperror.Error
			if errors.As(err, &he) {
				msg = he.Message
			}
			warn("skipped: " + msg)
			res.Skipped++
			continue
		}
		p := pending{q: q}
		for j, img := range q.Images {
			if j > 0 {
				warn(fmt.Sprintf("only the first picture is used; %q was left out", img.Filename))
				continue
			}
			if store == nil {
				warn(fmt.Sprintf("picture %q skipped: object storage is not configured", img.Filename))
				continue
			}
			ct, _, _ := mime.ParseMediaType(img.ContentType)
			ct = strings.ToLower(ct)
			if mediaTypes[ct] != domain.MediaImage {
				warn(fmt.Sprintf("picture %q skipped: unsupported image type", img.Filename))
				continue
			}
			size := int64(len(img.Data))
			if size == 0 || size > mediaMaxBytes[domain.MediaImage] {
				warn(fmt.Sprintf("picture %q skipped: empty or larger than %d MB", img.Filename, mediaMaxBytes[domain.MediaImage]>>20))
				continue
			}
			id := uuid.New()
			key := "courses/" + courseID.String() + "/media/" + id.String()
			if err := store.PutObject(ctx, key, bytes.NewReader(img.Data), size, ct); err != nil {
				warn(fmt.Sprintf("picture %q skipped: storing it failed", img.Filename))
				continue
			}
			name := strings.TrimSpace(img.Filename)
			if name == "" {
				name = "image"
			}
			if len(name) > 200 {
				name = name[:200]
			}
			im := importedImage{id: id, key: key, ct: ct, name: name, size: size}
			uploaded = append(uploaded, im)
			p.image = &im
		}
		todo = append(todo, p)
	}

	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		for _, p := range todo {
			var mediaID *uuid.UUID
			if p.image != nil {
				im := p.image
				if _, err := tx.Exec(ctx, `
					INSERT INTO media (id, course_id, kind, content_type, size_bytes, object_key, status, alt_text, name, created_by)
					VALUES ($1, $2, $3, $4, $5, $6, 'ready', $7, $7, $8)`,
					im.id, courseID, domain.MediaImage, im.ct, im.size, im.key, im.name, userID); err != nil {
					return err
				}
				mediaID = &im.id
				res.Images++
			}
			points := p.q.Points
			if points < 1 {
				points = 1
			}
			var qid uuid.UUID
			if err := tx.QueryRow(ctx, `
				INSERT INTO questions (category_id, kind, prompt, feedback, points, position, media_id)
				VALUES ($1, $2, $3, $4, $5, (SELECT COALESCE(MAX(position), -1) + 1 FROM questions WHERE category_id = $1), $6)
				RETURNING id`,
				categoryID, p.q.Kind, strings.TrimSpace(p.q.Prompt), strings.TrimSpace(p.q.Feedback), points, mediaID,
			).Scan(&qid); err != nil {
				return err
			}
			for i, o := range p.q.Options {
				if _, err := tx.Exec(ctx, `
					INSERT INTO question_options (question_id, label, correct, position) VALUES ($1, $2, $3, $4)`,
					qid, o.Label, o.Correct, i); err != nil {
					return err
				}
			}
			res.Imported++
		}
		return nil
	})
	if err != nil {
		if remover, ok := store.(interface {
			Remove(ctx context.Context, key string) error
		}); ok {
			for _, im := range uploaded {
				_ = remover.Remove(ctx, im.key)
			}
		}
		return ImportResult{}, err
	}
	return res, nil
}

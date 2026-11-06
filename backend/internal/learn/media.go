package learn

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AGlElectronics/learn/backend/internal/db"
	"github.com/AGlElectronics/learn/backend/internal/domain"
	"github.com/AGlElectronics/learn/backend/internal/files"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
)

const (
	mediaUploadExpiry = 15 * time.Minute
	// MediaViewExpiry is how long a presigned view link stays valid.
	MediaViewExpiry = 5 * time.Minute
	// Pending uploads older than this are labelled "upload not finished".
	mediaStaleAfter = 24 * time.Hour
)

// ObjectStore is the part of the object-storage client the media library
// needs. *files.Client implements it; tests can pass a fake.
type ObjectStore interface {
	PresignedPost(ctx context.Context, key, contentType string, maxBytes int64, expiry time.Duration) (string, map[string]string, error)
	PresignedGet(ctx context.Context, key string, expiry time.Duration) (string, error)
	Stat(ctx context.Context, key string) (files.ObjectInfo, error)
	Remove(ctx context.Context, key string) error
}

// mediaTypes maps every accepted content type to its media kind.
var mediaTypes = map[string]string{
	"image/png":   domain.MediaImage,
	"image/jpeg":  domain.MediaImage,
	"image/webp":  domain.MediaImage,
	"image/gif":   domain.MediaImage,
	"audio/mpeg":  domain.MediaAudio,
	"audio/mp3":   domain.MediaAudio,
	"audio/mp4":   domain.MediaAudio,
	"audio/x-m4a": domain.MediaAudio,
	"audio/m4a":   domain.MediaAudio,
	"audio/ogg":   domain.MediaAudio,
	"video/mp4":   domain.MediaVideo,
	"video/webm":  domain.MediaVideo,
}

// mediaMaxBytes is the size limit per kind.
var mediaMaxBytes = map[string]int64{
	domain.MediaImage: 10 << 20,
	domain.MediaAudio: 20 << 20,
	domain.MediaVideo: 200 << 20,
}

// MediaKindFor returns the media kind for a content type ("" if refused).
func MediaKindFor(contentType string) string {
	ct, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil {
		return ""
	}
	return mediaTypes[strings.ToLower(ct)]
}

// MediaRef is what content needs to render a file: the id to fetch a view
// link for, the kind to pick the player, and the alt text.
type MediaRef struct {
	ID          uuid.UUID `json:"id"`
	Kind        string    `json:"kind"`
	ContentType string    `json:"content_type"`
	AltText     string    `json:"alt_text"`
}

// Media is one media library entry (staff view).
type Media struct {
	ID          uuid.UUID  `json:"id"`
	CourseID    uuid.UUID  `json:"course_id"`
	Kind        string     `json:"kind"`
	ContentType string     `json:"content_type"`
	SizeBytes   int64      `json:"size_bytes"`
	Status      string     `json:"status"`
	AltText     string     `json:"alt_text"`
	Name        string     `json:"name"`
	CreatedBy   *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	// UsageCount counts questions, options and page attachments using it.
	UsageCount int  `json:"usage_count"`
	Unused     bool `json:"unused"`
	// UploadUnfinished: still pending more than 24 h after the upload began.
	UploadUnfinished bool `json:"upload_unfinished"`
}

const mediaUsageSQL = `(
	(SELECT count(*) FROM questions q WHERE q.media_id = m.id OR q.feedback_media_id = m.id)
	+ (SELECT count(*) FROM question_options o WHERE o.media_id = m.id)
	+ (SELECT count(*) FROM activity_media am WHERE am.media_id = m.id))::int`

const mediaCols = `m.id, m.course_id, m.kind, m.content_type, m.size_bytes, m.status, m.alt_text, m.name,
	m.created_by, m.created_at, m.updated_at, ` + mediaUsageSQL

func scanMedia(row pgx.Row, now time.Time) (Media, error) {
	var m Media
	err := row.Scan(&m.ID, &m.CourseID, &m.Kind, &m.ContentType, &m.SizeBytes, &m.Status, &m.AltText, &m.Name,
		&m.CreatedBy, &m.CreatedAt, &m.UpdatedAt, &m.UsageCount)
	m.Unused = m.UsageCount == 0
	m.UploadUnfinished = m.Status == "pending" && now.Sub(m.CreatedAt) > mediaStaleAfter
	return m, err
}

// MediaService is the media library. Storage is optional: with a nil store
// every operation that touches objects answers 503.
type MediaService struct {
	*Service
	store ObjectStore
	now   func() time.Time
}

// NewMediaService wraps the course service with an object store (may be nil).
func NewMediaService(svc *Service, store ObjectStore) *MediaService {
	return &MediaService{Service: svc, store: store, now: time.Now}
}

func (s *MediaService) requireStore() error {
	if s.store == nil {
		return httperror.New(httperror.CodeUnavailable, "media uploads are not available (object storage is not configured)")
	}
	return nil
}

// GetMedia loads one library entry.
func (s *MediaService) GetMedia(ctx context.Context, id uuid.UUID) (Media, error) {
	m, err := scanMedia(s.pool.QueryRow(ctx, `SELECT `+mediaCols+` FROM media m WHERE m.id = $1`, id), s.now())
	if errors.Is(err, pgx.ErrNoRows) {
		return m, httperror.NotFound("media")
	}
	return m, err
}

// MediaCourse resolves a media row's course for authorization.
func (s *Service) MediaCourse(ctx context.Context, mediaID uuid.UUID) (Course, error) {
	c, err := scanCourse(s.pool.QueryRow(ctx, `
		SELECT `+prefixedCourseCols("c")+`
		  FROM courses c JOIN media m ON m.course_id = c.id
		 WHERE m.id = $1`, mediaID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Course{}, httperror.NotFound("media")
	}
	return c, err
}

// ListMedia returns the course library, newest first. unusedOnly keeps
// rows nothing refers to (including unfinished uploads).
func (s *MediaService) ListMedia(ctx context.Context, courseID uuid.UUID, unusedOnly bool) ([]Media, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT * FROM (SELECT `+mediaCols+` AS usage_count FROM media m WHERE m.course_id = $1) x
		 WHERE NOT $2 OR x.usage_count = 0
		 ORDER BY x.created_at DESC`, courseID, unusedOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Media{}
	now := s.now()
	for rows.Next() {
		m, err := scanMedia(rows, now)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UploadInput announces a file before the browser uploads it.
type UploadInput struct {
	Name        string
	ContentType string
	SizeBytes   int64
	AltText     string
}

// UploadTicket is what the browser needs to POST the file to storage.
type UploadTicket struct {
	Media     Media             `json:"media"`
	URL       string            `json:"url"`
	Fields    map[string]string `json:"fields"`
	ExpiresAt time.Time         `json:"expires_at"`
}

// StartUpload checks type and size, records a pending row and returns a
// presigned POST whose policy pins the key, content type and size limit.
func (s *MediaService) StartUpload(ctx context.Context, courseID, userID uuid.UUID, in UploadInput) (UploadTicket, error) {
	if err := s.requireStore(); err != nil {
		return UploadTicket{}, err
	}
	ct, _, _ := mime.ParseMediaType(strings.TrimSpace(in.ContentType))
	ct = strings.ToLower(ct)
	kind := mediaTypes[ct]
	if kind == "" {
		return UploadTicket{}, httperror.Validation(
			"this file type isn't allowed (images: png, jpeg, webp, gif; audio: mp3, m4a, ogg; video: mp4, webm)", nil)
	}
	limit := mediaMaxBytes[kind]
	if in.SizeBytes <= 0 {
		return UploadTicket{}, httperror.Validation("the file is empty", nil)
	}
	if in.SizeBytes > limit {
		return UploadTicket{}, httperror.Validation(fmt.Sprintf("%s files can be at most %d MB", kind, limit>>20), nil)
	}
	name := strings.TrimSpace(in.Name)
	if len(name) > 200 {
		name = name[:200]
	}
	id := uuid.New()
	key := "courses/" + courseID.String() + "/media/" + id.String()
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO media (id, course_id, kind, content_type, size_bytes, object_key, alt_text, name, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, courseID, kind, ct, in.SizeBytes, key, strings.TrimSpace(in.AltText), name, userID); err != nil {
		return UploadTicket{}, err
	}
	postURL, fields, err := s.store.PresignedPost(ctx, key, ct, limit, mediaUploadExpiry)
	if err != nil {
		return UploadTicket{}, httperror.Internal(err)
	}
	m, err := s.GetMedia(ctx, id)
	if err != nil {
		return UploadTicket{}, err
	}
	return UploadTicket{Media: m, URL: postURL, Fields: fields, ExpiresAt: s.now().Add(mediaUploadExpiry)}, nil
}

// CompleteUpload confirms the object arrived with an allowed size and the
// announced type, then marks the row ready. Completing twice is harmless.
// A file that fails the check stays pending for staff to delete.
func (s *MediaService) CompleteUpload(ctx context.Context, id uuid.UUID) (Media, error) {
	if err := s.requireStore(); err != nil {
		return Media{}, err
	}
	var key, kind, ct, status string
	err := s.pool.QueryRow(ctx, `SELECT object_key, kind, content_type, status FROM media WHERE id = $1`, id).
		Scan(&key, &kind, &ct, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Media{}, httperror.NotFound("media")
	}
	if err != nil {
		return Media{}, err
	}
	if status == "ready" {
		return s.GetMedia(ctx, id)
	}
	info, err := s.store.Stat(ctx, key)
	if err != nil {
		return Media{}, httperror.Conflict("the file hasn't arrived in storage yet; upload it first")
	}
	if info.Size <= 0 || info.Size > mediaMaxBytes[kind] {
		return Media{}, httperror.Validation("the stored file is empty or too large", nil)
	}
	if got, _, _ := mime.ParseMediaType(info.ContentType); !strings.EqualFold(got, ct) {
		return Media{}, httperror.Validation("the stored file's type does not match the announced type", nil)
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE media SET status = 'ready', size_bytes = $2, updated_at = now() WHERE id = $1`, id, info.Size); err != nil {
		return Media{}, err
	}
	return s.GetMedia(ctx, id)
}

// UpdateMedia edits alt text and the staff-facing name. An image that is in
// use can't lose its alt text.
func (s *MediaService) UpdateMedia(ctx context.Context, id uuid.UUID, altText, name *string) (Media, error) {
	cur, err := s.GetMedia(ctx, id)
	if err != nil {
		return Media{}, err
	}
	if altText != nil {
		*altText = strings.TrimSpace(*altText)
		if *altText == "" && cur.Kind == domain.MediaImage && cur.UsageCount > 0 {
			return Media{}, httperror.Validation("an image in use needs alt text", nil)
		}
	}
	if name != nil {
		*name = strings.TrimSpace(*name)
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE media SET alt_text = COALESCE($2, alt_text), name = COALESCE($3, name), updated_at = now()
		 WHERE id = $1`, id, altText, name); err != nil {
		return Media{}, err
	}
	return s.GetMedia(ctx, id)
}

// DeleteMedia removes an unused file. Refused (409) while anything refers
// to it. The stored object is removed only when no other media row (a
// copied course) still points at it. This is the only way media is ever
// deleted.
func (s *MediaService) DeleteMedia(ctx context.Context, id uuid.UUID) error {
	m, err := s.GetMedia(ctx, id)
	if err != nil {
		return err
	}
	if m.UsageCount > 0 {
		return httperror.Conflict(fmt.Sprintf("this file is used in %d place(s); remove it there first", m.UsageCount))
	}
	if err := s.requireStore(); err != nil {
		return err
	}
	var key string
	var shared bool
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT object_key FROM media WHERE id = $1`, id).Scan(&key); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return httperror.NotFound("media")
			}
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "learn-media-object:"+key); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM media WHERE id = $1`, id); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM media WHERE object_key = $1)`, key).Scan(&shared)
	})
	var fk interface{ SQLState() string }
	if errors.As(err, &fk) && fk.SQLState() == "23503" {
		return httperror.Conflict("this file was just attached somewhere; remove it there first")
	}
	if err != nil || shared {
		return err
	}
	if err := s.store.Remove(ctx, key); err != nil {
		return httperror.Internal(err)
	}
	return nil
}

// ViewURL returns a short-lived presigned link to a ready file.
func (s *MediaService) ViewURL(ctx context.Context, id uuid.UUID) (string, time.Time, error) {
	var key, status string
	err := s.pool.QueryRow(ctx, `SELECT object_key, status FROM media WHERE id = $1`, id).Scan(&key, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", time.Time{}, httperror.NotFound("media")
	}
	if err != nil {
		return "", time.Time{}, err
	}
	if err := s.requireStore(); err != nil {
		return "", time.Time{}, err
	}
	if status != "ready" {
		return "", time.Time{}, httperror.Conflict("this upload is not finished")
	}
	u, err := s.store.PresignedGet(ctx, key, MediaViewExpiry)
	if err != nil {
		return "", time.Time{}, httperror.Internal(err)
	}
	return u, s.now().Add(MediaViewExpiry), nil
}

// LearnerMayView decides whether a non-staff person enrolled on the
// media's (published) course may see it: it must be ready and used by a
// question, or attached to a page that isn't locked for them. Media used
// only by locked pages answers 403 locked; anything else 404.
func (s *Service) LearnerMayView(ctx context.Context, mediaID, userID uuid.UUID) error {
	var status string
	var inQuestion bool
	var activityIDs []uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT m.status,
		       EXISTS (SELECT 1 FROM questions q WHERE q.media_id = m.id OR q.feedback_media_id = m.id)
		       OR EXISTS (SELECT 1 FROM question_options o WHERE o.media_id = m.id),
		       ARRAY(SELECT DISTINCT am.activity_id FROM activity_media am WHERE am.media_id = m.id)
		  FROM media m WHERE m.id = $1`, mediaID).Scan(&status, &inQuestion, &activityIDs)
	if errors.Is(err, pgx.ErrNoRows) {
		return httperror.NotFound("media")
	}
	if err != nil {
		return err
	}
	if status != "ready" {
		return httperror.NotFound("media")
	}
	if inQuestion {
		return nil
	}
	if len(activityIDs) == 0 {
		return httperror.NotFound("media")
	}
	var needs []string
	for _, aid := range activityIDs {
		locked, n, err := s.ActivityLock(ctx, aid, userID)
		if err != nil {
			return err
		}
		if !locked {
			return nil
		}
		needs = append(needs, n...)
	}
	return lockedError(needs)
}

// ----------------------------------------------------------------------------
// Attaching media to content
// ----------------------------------------------------------------------------

// queryer is satisfied by the pool and by transactions.
type queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// attachableMedia checks that a file may be attached to content of a
// course: same course, upload finished, alt text on images, and (when
// kinds is non-empty) one of the allowed kinds.
func attachableMedia(ctx context.Context, q queryer, courseID, mediaID uuid.UUID, kinds ...string) (MediaRef, error) {
	var ref MediaRef
	var owner uuid.UUID
	var status string
	err := q.QueryRow(ctx, `SELECT id, kind, content_type, alt_text, course_id, status FROM media WHERE id = $1`, mediaID).
		Scan(&ref.ID, &ref.Kind, &ref.ContentType, &ref.AltText, &owner, &status)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != courseID) {
		return ref, httperror.Validation("the media file must belong to this course", nil)
	}
	if err != nil {
		return ref, err
	}
	if status != "ready" {
		return ref, httperror.Validation("this upload is not finished", nil)
	}
	if ref.Kind == domain.MediaImage && strings.TrimSpace(ref.AltText) == "" {
		return ref, httperror.Validation("add alt text to the image before using it", nil)
	}
	if len(kinds) > 0 {
		ok := false
		for _, k := range kinds {
			ok = ok || k == ref.Kind
		}
		if !ok {
			return ref, httperror.Validation("this needs a file of kind "+strings.Join(kinds, " or "), nil)
		}
	}
	return ref, nil
}

// mediaRefs loads render info for a set of media ids.
func (s *Service) mediaRefs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]MediaRef, error) {
	out := map[uuid.UUID]MediaRef{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id, kind, content_type, alt_text FROM media WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r MediaRef
		if err := rows.Scan(&r.ID, &r.Kind, &r.ContentType, &r.AltText); err != nil {
			return nil, err
		}
		out[r.ID] = r
	}
	return out, rows.Err()
}

func refFor(refs map[uuid.UUID]MediaRef, id *uuid.UUID) *MediaRef {
	if id == nil {
		return nil
	}
	if r, ok := refs[*id]; ok {
		return &r
	}
	return nil
}

// ----------------------------------------------------------------------------
// Page attachments
// ----------------------------------------------------------------------------

// ActivityMedia is one attachment shown under a page: an uploaded file, or
// an external video played through a privacy-friendly embed.
type ActivityMedia struct {
	ID          uuid.UUID  `json:"id"`
	ActivityID  uuid.UUID  `json:"activity_id"`
	MediaID     *uuid.UUID `json:"media_id"`
	Media       *MediaRef  `json:"media,omitempty"`
	ExternalURL *string    `json:"external_url"`
	// EmbedURL and Provider are set for external videos.
	EmbedURL string `json:"embed_url,omitempty"`
	Provider string `json:"provider,omitempty"`
	Position int    `json:"position"`
	Caption  string `json:"caption"`
}

var (
	youtubeID = regexp.MustCompile(`^[A-Za-z0-9_-]{6,20}$`)
	vimeoID   = regexp.MustCompile(`^[0-9]{1,15}$`)
)

// EmbedURL turns a YouTube or Vimeo link into its privacy-friendly player
// URL. Every other host is refused.
func EmbedURL(raw string) (embed, provider string, err error) {
	bad := httperror.Validation("only YouTube and Vimeo video links can be embedded", nil)
	u, perr := url.Parse(strings.TrimSpace(raw))
	if perr != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return "", "", bad
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(strings.TrimPrefix(host, "www."), "m.")
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	var id string
	switch host {
	case "youtu.be":
		id = parts[0]
		provider = "youtube"
	case "youtube.com", "youtube-nocookie.com":
		provider = "youtube"
		switch {
		case len(parts) == 1 && parts[0] == "watch":
			id = u.Query().Get("v")
		case len(parts) == 2 && (parts[0] == "embed" || parts[0] == "shorts" || parts[0] == "live"):
			id = parts[1]
		}
	case "vimeo.com":
		provider = "vimeo"
		id = parts[len(parts)-1]
	case "player.vimeo.com":
		provider = "vimeo"
		if len(parts) == 2 && parts[0] == "video" {
			id = parts[1]
		}
	default:
		return "", "", bad
	}
	switch provider {
	case "youtube":
		if !youtubeID.MatchString(id) {
			return "", "", httperror.Validation("this YouTube link has no video id", nil)
		}
		return "https://www.youtube-nocookie.com/embed/" + id, provider, nil
	default:
		if !vimeoID.MatchString(id) {
			return "", "", httperror.Validation("this Vimeo link has no video id", nil)
		}
		return "https://player.vimeo.com/video/" + id + "?dnt=1", provider, nil
	}
}

// ListActivityMedia returns a page's attachments in order.
func (s *Service) ListActivityMedia(ctx context.Context, activityID uuid.UUID) ([]ActivityMedia, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, activity_id, media_id, external_url, position, caption
		  FROM activity_media WHERE activity_id = $1
		 ORDER BY position, created_at`, activityID)
	if err != nil {
		return nil, err
	}
	out := []ActivityMedia{}
	ids := []uuid.UUID{}
	for rows.Next() {
		var am ActivityMedia
		if err := rows.Scan(&am.ID, &am.ActivityID, &am.MediaID, &am.ExternalURL, &am.Position, &am.Caption); err != nil {
			rows.Close()
			return nil, err
		}
		if am.MediaID != nil {
			ids = append(ids, *am.MediaID)
		}
		out = append(out, am)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	refs, err := s.mediaRefs(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Media = refFor(refs, out[i].MediaID)
		if out[i].ExternalURL != nil {
			out[i].EmbedURL, out[i].Provider, _ = EmbedURL(*out[i].ExternalURL)
		}
	}
	return out, nil
}

// AddActivityMediaInput is either MediaID or ExternalURL.
type AddActivityMediaInput struct {
	MediaID     *uuid.UUID
	ExternalURL *string
	Caption     string
}

// AddActivityMedia appends an attachment to a page.
func (s *Service) AddActivityMedia(ctx context.Context, activityID uuid.UUID, in AddActivityMediaInput) (ActivityMedia, error) {
	var kind string
	var courseID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT a.kind, s.course_id FROM activities a JOIN sections s ON s.id = a.section_id WHERE a.id = $1`,
		activityID).Scan(&kind, &courseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ActivityMedia{}, httperror.NotFound("activity")
	}
	if err != nil {
		return ActivityMedia{}, err
	}
	if kind != domain.ActivityPage {
		return ActivityMedia{}, httperror.Validation("only pages have attachments", nil)
	}
	switch {
	case in.MediaID != nil && in.ExternalURL == nil:
		if _, err := attachableMedia(ctx, s.pool, courseID, *in.MediaID); err != nil {
			return ActivityMedia{}, err
		}
	case in.MediaID == nil && in.ExternalURL != nil:
		*in.ExternalURL = strings.TrimSpace(*in.ExternalURL)
		if _, _, err := EmbedURL(*in.ExternalURL); err != nil {
			return ActivityMedia{}, err
		}
	default:
		return ActivityMedia{}, httperror.Validation("give either media_id or external_url", nil)
	}
	var id uuid.UUID
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO activity_media (activity_id, media_id, external_url, caption, position)
		VALUES ($1, $2, $3, $4, (SELECT COALESCE(MAX(position), -1) + 1 FROM activity_media WHERE activity_id = $1))
		RETURNING id`, activityID, in.MediaID, in.ExternalURL, strings.TrimSpace(in.Caption)).Scan(&id); err != nil {
		return ActivityMedia{}, err
	}
	return s.getActivityMedia(ctx, id)
}

func (s *Service) getActivityMedia(ctx context.Context, id uuid.UUID) (ActivityMedia, error) {
	var activityID uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT activity_id FROM activity_media WHERE id = $1`, id).Scan(&activityID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ActivityMedia{}, httperror.NotFound("attachment")
	}
	if err != nil {
		return ActivityMedia{}, err
	}
	all, err := s.ListActivityMedia(ctx, activityID)
	if err != nil {
		return ActivityMedia{}, err
	}
	for _, am := range all {
		if am.ID == id {
			return am, nil
		}
	}
	return ActivityMedia{}, httperror.NotFound("attachment")
}

// UpdateActivityMedia edits an attachment's caption or position.
func (s *Service) UpdateActivityMedia(ctx context.Context, id uuid.UUID, caption *string, position *int) (ActivityMedia, error) {
	if caption != nil {
		*caption = strings.TrimSpace(*caption)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE activity_media SET caption = COALESCE($2, caption), position = COALESCE($3, position)
		 WHERE id = $1`, id, caption, position)
	if err != nil {
		return ActivityMedia{}, err
	}
	if tag.RowsAffected() == 0 {
		return ActivityMedia{}, httperror.NotFound("attachment")
	}
	return s.getActivityMedia(ctx, id)
}

// DeleteActivityMedia removes an attachment. The file stays in the library.
func (s *Service) DeleteActivityMedia(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM activity_media WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return httperror.NotFound("attachment")
	}
	return err
}

// ActivityMediaCourse resolves an attachment's course and activity.
func (s *Service) ActivityMediaCourse(ctx context.Context, id uuid.UUID) (Course, error) {
	c, err := scanCourse(s.pool.QueryRow(ctx, `
		SELECT `+prefixedCourseCols("c")+`
		  FROM courses c
		  JOIN sections s ON s.course_id = c.id
		  JOIN activities a ON a.section_id = s.id
		  JOIN activity_media am ON am.activity_id = a.id
		 WHERE am.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Course{}, httperror.NotFound("attachment")
	}
	return c, err
}

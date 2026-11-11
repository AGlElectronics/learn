// Package keycloakadmin is a read-only Keycloak Admin REST client Learn uses
// to resolve groups and their members (e.g. assigning a quiz to everyone in
// a group). It authenticates as a confidential client with a service account
// (learn-provisioner) holding realm-management view-users + query-groups.
package keycloakadmin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AGlElectronics/learn/backend/internal/config"
)

// Config is where and as whom to call the Admin API.
type Config struct {
	// BaseURL is the Keycloak origin reachable from Learn (e.g. http://keycloak:8080).
	BaseURL      string
	Realm        string
	ClientID     string
	ClientSecret string
	// PageSize bounds each paged request (default 100).
	PageSize   int
	HTTPClient *http.Client
}

// Group is a realm group. Path is the full slash-separated path ("/a/b").
type Group struct {
	ID   string
	Name string
	Path string
}

// Member is a user in a group.
type Member struct {
	ID        string
	Username  string
	Email     string
	FirstName string
	LastName  string
	Enabled   bool
}

// Client calls the Keycloak Admin API with a cached service-account token.
type Client struct {
	cfg Config

	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
}

// ErrGroupNotFound is returned by GroupMembers for an unknown group.
var ErrGroupNotFound = errors.New("keycloak group not found")

// New validates cfg and returns a Client.
func New(cfg Config) (*Client, error) {
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.Realm = strings.TrimSpace(cfg.Realm)
	if cfg.BaseURL == "" || cfg.Realm == "" || cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, errors.New("keycloakadmin: BaseURL, Realm, ClientID and ClientSecret are required")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 20 * time.Second}
	}
	if cfg.PageSize <= 0 {
		cfg.PageSize = 100
	}
	return &Client{cfg: cfg}, nil
}

// NewFromConfig returns (nil, nil) when the KEYCLOAK_* settings are not all set.
func NewFromConfig(c *config.Config) (*Client, error) {
	if !c.KeycloakAdminEnabled() {
		return nil, nil
	}
	return New(Config{
		BaseURL:      c.KeycloakBaseURL,
		Realm:        c.KeycloakRealm,
		ClientID:     c.KeycloakClientID,
		ClientSecret: c.KeycloakClientSecret,
	})
}

// AdminError is a non-2xx Keycloak Admin API response.
type AdminError struct {
	Method, Path string
	Status       int
	Body         string
}

func (e *AdminError) Error() string {
	return fmt.Sprintf("keycloak admin %s %s: status %d: %s", e.Method, e.Path, e.Status, e.Body)
}

var errUnauthorized = errors.New("keycloak admin: unauthorized")

type kcGroup struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	SubGroups []kcGroup `json:"subGroups"`
	// SubGroupCount is only sent by Keycloak 23+.
	SubGroupCount *int `json:"subGroupCount"`
}

type kcUser struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Enabled   bool   `json:"enabled"`
}

// ListGroups returns every realm group, subgroups flattened depth-first.
func (c *Client) ListGroups(ctx context.Context) ([]Group, error) {
	var out []Group
	err := c.withFreshTokenRetry(ctx, func(token string) error {
		out = out[:0]
		var walk func(gs []kcGroup) error
		walk = func(gs []kcGroup) error {
			for _, g := range gs {
				out = append(out, Group{ID: g.ID, Name: g.Name, Path: g.Path})
				subs := g.SubGroups
				if len(subs) == 0 && (g.SubGroupCount == nil || *g.SubGroupCount > 0) {
					// Newer Keycloak versions omit subGroups from list
					// responses; fetch children explicitly.
					var err error
					subs, err = c.children(ctx, token, g.ID)
					if err != nil {
						return err
					}
				}
				if err := walk(subs); err != nil {
					return err
				}
			}
			return nil
		}
		top, err := pageAll[kcGroup](ctx, c, token, "/groups?briefRepresentation=true")
		if err != nil {
			return err
		}
		return walk(top)
	})
	return out, err
}

func (c *Client) children(ctx context.Context, token, groupID string) ([]kcGroup, error) {
	subs, err := pageAll[kcGroup](ctx, c, token, "/groups/"+url.PathEscape(groupID)+"/children?briefRepresentation=true")
	var ae *AdminError
	if errors.As(err, &ae) && (ae.Status == http.StatusNotFound || ae.Status == http.StatusMethodNotAllowed) {
		return nil, nil // older Keycloak: subGroups were inline, nothing to fetch
	}
	return subs, err
}

// GroupMembers returns the direct members of the group at groupPath, a full
// path such as "/learn/cohort-a". Paths compare like token groups do
// (ignoring case and the leading "/"), so a top-level group matches its
// plain name too. ErrGroupNotFound if absent.
func (c *Client) GroupMembers(ctx context.Context, groupPath string) ([]Member, error) {
	want := pathKey(groupPath)
	groups, err := c.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	id := ""
	for _, g := range groups {
		if pathKey(g.Path) == want {
			id = g.ID
			break
		}
	}
	if id == "" {
		return nil, fmt.Errorf("%w: %q", ErrGroupNotFound, groupPath)
	}
	var out []Member
	err = c.withFreshTokenRetry(ctx, func(token string) error {
		users, err := pageAll[kcUser](ctx, c, token, "/groups/"+url.PathEscape(id)+"/members?briefRepresentation=true")
		if err != nil {
			return err
		}
		out = make([]Member, 0, len(users))
		for _, u := range users {
			out = append(out, Member(u))
		}
		return nil
	})
	return out, err
}

// SearchUsers returns up to max enabled users whose username, name or email
// contains q (Keycloak's search parameter).
func (c *Client) SearchUsers(ctx context.Context, q string, max int) ([]Member, error) {
	if max <= 0 || max > 50 {
		max = 20
	}
	var out []Member
	err := c.withFreshTokenRetry(ctx, func(token string) error {
		var users []kcUser
		p := "/users?briefRepresentation=true&enabled=true&search=" + url.QueryEscape(q) + "&max=" + strconv.Itoa(max)
		if err := c.getJSON(ctx, token, p, &users); err != nil {
			return err
		}
		out = make([]Member, 0, len(users))
		for _, u := range users {
			out = append(out, Member(u))
		}
		return nil
	})
	return out, err
}

// ErrUserNotFound is returned by GetUser for an unknown user id.
var ErrUserNotFound = errors.New("keycloak user not found")

// GetUser returns one user by Keycloak id.
func (c *Client) GetUser(ctx context.Context, id string) (Member, error) {
	var out Member
	err := c.withFreshTokenRetry(ctx, func(token string) error {
		var u kcUser
		if err := c.getJSON(ctx, token, "/users/"+url.PathEscape(id), &u); err != nil {
			var ae *AdminError
			if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
				return fmt.Errorf("%w: %q", ErrUserNotFound, id)
			}
			return err
		}
		if u.ID == "" {
			return fmt.Errorf("%w: %q", ErrUserNotFound, id)
		}
		out = Member(u)
		return nil
	})
	return out, err
}

// UserGroups returns the groups the user is a direct member of.
func (c *Client) UserGroups(ctx context.Context, userID string) ([]Group, error) {
	var out []Group
	err := c.withFreshTokenRetry(ctx, func(token string) error {
		gs, err := pageAll[kcGroup](ctx, c, token, "/users/"+url.PathEscape(userID)+"/groups?briefRepresentation=true")
		if err != nil {
			return err
		}
		out = make([]Group, 0, len(gs))
		for _, g := range gs {
			out = append(out, Group{ID: g.ID, Name: g.Name, Path: g.Path})
		}
		return nil
	})
	return out, err
}

// pageAll fetches path with first/max paging until a short page.
func pageAll[T any](ctx context.Context, c *Client, token, path string) ([]T, error) {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	var out []T
	for first := 0; ; first += c.cfg.PageSize {
		var page []T
		p := path + sep + "first=" + strconv.Itoa(first) + "&max=" + strconv.Itoa(c.cfg.PageSize)
		if err := c.getJSON(ctx, token, p, &page); err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < c.cfg.PageSize {
			return out, nil
		}
	}
}

// withFreshTokenRetry runs op, and once more with a new service-account
// token if Keycloak rejected the cached one (e.g. after a Keycloak restart).
func (c *Client) withFreshTokenRetry(ctx context.Context, op func(token string) error) error {
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	err = op(token)
	if errors.Is(err, errUnauthorized) {
		c.mu.Lock()
		c.token = ""
		c.mu.Unlock()
		if token, err = c.accessToken(ctx); err != nil {
			return err
		}
		err = op(token)
	}
	return err
}

func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExpiry.Add(-30*time.Second)) {
		return c.token, nil
	}
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", c.cfg.ClientID)
	form.Set("client_secret", c.cfg.ClientSecret)
	endpoint := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", c.cfg.BaseURL, url.PathEscape(c.cfg.Realm))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("keycloak token: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return "", fmt.Errorf("keycloak token: status %d: %s", res.StatusCode, truncate(string(body), 300))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("keycloak token decode: %w", err)
	}
	if tok.AccessToken == "" {
		return "", errors.New("keycloak token: empty access_token")
	}
	c.token = tok.AccessToken
	exp := tok.ExpiresIn
	if exp <= 0 {
		exp = 60
	}
	c.tokenExpiry = time.Now().Add(time.Duration(exp) * time.Second)
	return c.token, nil
}

func (c *Client) getJSON(ctx context.Context, token, path string, dest any) error {
	endpoint := fmt.Sprintf("%s/admin/realms/%s%s", c.cfg.BaseURL, url.PathEscape(c.cfg.Realm), path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	res, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("keycloak admin GET %s: %w", path, err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("%w: GET %s", errUnauthorized, path)
	}
	if res.StatusCode >= 300 {
		return &AdminError{Method: http.MethodGet, Path: path, Status: res.StatusCode, Body: truncate(string(body), 300)}
	}
	if len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return fmt.Errorf("keycloak admin decode %s: %w", path, err)
	}
	return nil
}

// pathKey is the comparison form of a group path: trimmed, one leading "/"
// dropped, lower-case.
func pathKey(p string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(p), "/"))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

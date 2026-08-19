package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/auth"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/config"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
	"gorm.io/gorm"
)

// mockOAuthRepo is an in-memory GoogleOAuthRepository for unit tests (SEC-27
// narrow-interface mocking — no real DB).
type mockOAuthRepo struct {
	states       map[string]*models.OAuthState
	usersBySub   map[string]*models.User
	usersByEmail map[string]*models.User
	usersByID    map[uuid.UUID]*models.User
	consumed     map[string]bool

	consumeWon bool // controls whether ConsumeOAuthState wins the race

	linkedSub    string
	linkedUserID string
	createdUser  *models.User
	createErr    error
}

func newMockOAuthRepo() *mockOAuthRepo {
	return &mockOAuthRepo{
		states:       map[string]*models.OAuthState{},
		usersBySub:   map[string]*models.User{},
		usersByEmail: map[string]*models.User{},
		usersByID:    map[uuid.UUID]*models.User{},
		consumed:     map[string]bool{},
		consumeWon:   true,
	}
}

func (m *mockOAuthRepo) CreateOAuthState(_ context.Context, s *models.OAuthState) error {
	m.states[s.StateHash] = s
	return nil
}

func (m *mockOAuthRepo) ConsumeOAuthState(_ context.Context, hash string) (models.OAuthState, bool, error) {
	row, ok := m.states[hash]
	if !ok || m.consumed[hash] || row.ExpiresAt.Before(time.Now()) || !m.consumeWon {
		return models.OAuthState{}, false, nil
	}
	m.consumed[hash] = true
	return *row, true, nil
}

func (m *mockOAuthRepo) DeleteExpiredOAuthStates(_ context.Context, before time.Time) (int64, error) {
	var n int64
	for k, v := range m.states {
		if v.ExpiresAt.Before(before) {
			delete(m.states, k)
			n++
		}
	}
	return n, nil
}

func (m *mockOAuthRepo) FindUserByGoogleSub(_ context.Context, sub string) (models.User, error) {
	if u, ok := m.usersBySub[sub]; ok {
		return *u, nil
	}
	return models.User{}, gorm.ErrRecordNotFound
}

func (m *mockOAuthRepo) LinkUserGoogleSub(_ context.Context, userID string, sub string) error {
	m.linkedSub = sub
	m.linkedUserID = userID
	if id, err := uuid.Parse(userID); err == nil {
		if u, ok := m.usersByID[id]; ok {
			u.GoogleSub = &sub
			m.usersBySub[sub] = u
		}
	}
	return nil
}

func (m *mockOAuthRepo) FindUserByEmail(_ context.Context, email string) (models.User, error) {
	if u, ok := m.usersByEmail[email]; ok {
		return *u, nil
	}
	return models.User{}, gorm.ErrRecordNotFound
}

func (m *mockOAuthRepo) CreateUser(_ context.Context, u *models.User) error {
	if m.createErr != nil {
		return m.createErr
	}
	u.ID = uuid.New()
	m.createdUser = u
	m.usersByEmail[u.Email] = u
	if u.GoogleSub != nil {
		m.usersBySub[*u.GoogleSub] = u
	}
	m.usersByID[u.ID] = u
	return nil
}

func (m *mockOAuthRepo) FirstOrCreateUser(_ context.Context, u *models.User) error { return nil }
func (m *mockOAuthRepo) FindUserByID(_ context.Context, id uuid.UUID) (models.User, error) {
	if u, ok := m.usersByID[id]; ok {
		return *u, nil
	}
	return models.User{}, gorm.ErrRecordNotFound
}

func TestSanitizeReturnTo(t *testing.T) {
	cases := map[string]string{
		"":                     "/",
		"/":                    "/",
		"/trip/abc":            "/trip/abc",
		"/order/1?x=1":         "/order/1?x=1",
		"//evil.com":           "/",
		"https://evil.com":     "/",
		"javascript:alert(1)":  "/",
		"  /login  ":           "/login",
		"/path\r\nSet-Cookie:": "/",
		"relative":             "/",
	}
	for in, want := range cases {
		if got := sanitizeReturnTo(in); got != want {
			t.Errorf("sanitizeReturnTo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStartLogin_PersistsHashedStateOnly(t *testing.T) {
	repo := newMockOAuthRepo()
	svc := &GoogleOAuthService{repo: repo, google: auth.NewGoogleClient("cid", "secret", "http://localhost/cb"), cfg: testCfg()}

	res, err := svc.StartLogin(context.Background(), "/trip/abc")
	if err != nil {
		t.Fatalf("StartLogin err: %v", err)
	}
	if res.RedirectURL == "" {
		t.Fatal("expected redirect URL")
	}
	if len(repo.states) != 1 {
		t.Fatalf("expected 1 persisted state, got %d", len(repo.states))
	}
	for hash, row := range repo.states {
		if len(hash) != 64 { // sha256 hex
			t.Errorf("state key is not a sha256 hex digest: %q", hash)
		}
		if row.ReturnTo != "/trip/abc" {
			t.Errorf("return_to = %q", row.ReturnTo)
		}
		if row.Nonce == "" {
			t.Error("nonce empty")
		}
		if row.ExpiresAt.Before(time.Now()) {
			t.Error("state already expired")
		}
	}
}

func TestCallback_RejectsUnknownOrReplayedState(t *testing.T) {
	repo := newMockOAuthRepo()
	svc := &GoogleOAuthService{repo: repo, google: auth.NewGoogleClient("cid", "secret", "http://localhost/cb"), cfg: testCfg()}

	// Unknown state.
	if _, err := svc.Callback(context.Background(), "code", "nope", AuthRequestMeta{}); !errors.Is(err, ErrGoogleOAuthStateInvalid) {
		t.Fatalf("expected ErrGoogleOAuthStateInvalid for unknown state, got %v", err)
	}

	// Valid state but consume race lost (simulate replay/used state).
	repo.consumeWon = false
	state, _ := randomURLToken(32)
	repo.states[hashOAuthState(state)] = &models.OAuthState{
		StateHash: hashOAuthState(state),
		Nonce:     "n",
		ReturnTo:  "/",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	if _, err := svc.Callback(context.Background(), "code", state, AuthRequestMeta{}); !errors.Is(err, ErrGoogleOAuthStateInvalid) {
		t.Fatalf("expected ErrGoogleOAuthStateInvalid for lost consume race, got %v", err)
	}
}

func TestResolveUser_BySub(t *testing.T) {
	repo := newMockOAuthRepo()
	existing := &models.User{Name: "A", Email: "a@x.com", Role: models.RoleUser}
	existing.ID = uuid.New()
	sub := "google-sub-1"
	existing.GoogleSub = &sub
	repo.usersBySub[sub] = existing

	svc := &GoogleOAuthService{repo: repo, cfg: testCfg()}
	u, err := svc.resolveUser(context.Background(), auth.GoogleIdentity{Subject: sub, Email: "a@x.com", EmailVerified: true}, AuthRequestMeta{})
	if err != nil {
		t.Fatalf("resolveUser err: %v", err)
	}
	if u.ID != existing.ID {
		t.Errorf("expected existing user by sub, got %v", u.ID)
	}
}

func TestResolveUser_LinkByVerifiedEmail(t *testing.T) {
	repo := newMockOAuthRepo()
	existing := &models.User{Name: "B", Email: "b@x.com", Role: models.RoleUser}
	existing.ID = uuid.New()
	repo.usersByEmail["b@x.com"] = existing
	repo.usersByID[existing.ID] = existing

	svc := &GoogleOAuthService{repo: repo, cfg: testCfg()}
	u, err := svc.resolveUser(context.Background(), auth.GoogleIdentity{Subject: "newsub", Email: "b@x.com", EmailVerified: true}, AuthRequestMeta{})
	if err != nil {
		t.Fatalf("resolveUser err: %v", err)
	}
	if repo.linkedSub != "newsub" {
		t.Errorf("expected link google_sub=newsub, got %q", repo.linkedSub)
	}
	if repo.linkedUserID != existing.ID.String() {
		t.Errorf("linked wrong user %q", repo.linkedUserID)
	}
	if u.GoogleSub == nil || *u.GoogleSub != "newsub" {
		t.Error("returned user does not reflect link")
	}
}

func TestResolveUser_CreateNew(t *testing.T) {
	repo := newMockOAuthRepo()
	svc := &GoogleOAuthService{repo: repo, cfg: testCfg()}
	u, err := svc.resolveUser(context.Background(), auth.GoogleIdentity{Subject: "s3", Email: "c@x.com", Name: "Cee", EmailVerified: true}, AuthRequestMeta{})
	if err != nil {
		t.Fatalf("resolveUser err: %v", err)
	}
	if repo.createdUser == nil {
		t.Fatal("expected CreateUser called")
	}
	if u.Role != models.RoleUser {
		t.Errorf("new Google user must be RoleUser, got %q", u.Role)
	}
	if u.GoogleSub == nil || *u.GoogleSub != "s3" {
		t.Error("new user missing google_sub")
	}
	if u.Password == "" {
		t.Error("new user password empty (must be random bcrypt placeholder)")
	}
	if u.Name != "Cee" {
		t.Errorf("name from Google claim = %q", u.Name)
	}
}

func TestResolveUser_CreateRaceFallsBackToExisting(t *testing.T) {
	repo := newMockOAuthRepo()
	repo.createErr = errors.New("unique constraint")
	// The "other" parallel callback already created the user by email.
	winner := &models.User{Name: "D", Email: "d@x.com", Role: models.RoleUser}
	winner.ID = uuid.New()
	sub := "s4"
	winner.GoogleSub = &sub
	repo.usersByEmail["d@x.com"] = winner
	repo.usersBySub[sub] = winner

	svc := &GoogleOAuthService{repo: repo, cfg: testCfg()}
	u, err := svc.resolveUser(context.Background(), auth.GoogleIdentity{Subject: sub, Email: "d@x.com", EmailVerified: true}, AuthRequestMeta{})
	if err != nil {
		t.Fatalf("expected fallback to existing user, got err %v", err)
	}
	if u.ID != winner.ID {
		t.Errorf("expected winner user, got %v", u.ID)
	}
}

func testCfg() config.Config { return config.Config{} }

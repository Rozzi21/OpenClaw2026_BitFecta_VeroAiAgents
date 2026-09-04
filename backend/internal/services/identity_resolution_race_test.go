package services

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/auth"
	"github.com/rozzi/vero-ai-travel-agents/backend/internal/models"
)

// TOCTOU tests for account/identity resolution (P1-H1).
//
// resolveUser is read-read-write: (1) look up the Google sub, (2) look up the
// email, (3) create. A parallel Google callback or POST /auth/register can commit
// between (2) and (3), so the create fails on a unique index and the FALLBACK
// decides who the caller becomes. That fallback used to resolve by EMAIL, which
// silently produced exactly the merge the guard at step (2) exists to refuse —
// and the guest-order claim that runs right after the callback then moved the
// caller's guest order into the account it landed on.
//
// The rule these tests pin: a fallback may only re-resolve through the SAME key
// the primary lookup used (the immutable Google sub), and while that key is still
// absent the answer must be the SAME refusal the pre-create guard gives.

// TestResolveUser_CreateRaceNeverMergesByEmail: the create loses the race, the
// sub is still unlinked, and an account with that email exists. Refuse — never
// hand back a session on an account this Google identity was never linked to.
func TestResolveUser_CreateRaceNeverMergesByEmail(t *testing.T) {
	repo := newMockOAuthRepo()
	repo.createErr = errors.New(`duplicate key value violates unique constraint "users_email_key"`)
	// The account that won the race is a PASSWORD account: same email, no
	// google_sub, no external identity.
	victim := &models.User{Name: "Victim", Email: "victim@example.com", Role: models.RoleUser}
	victim.ID = uuid.New()
	repo.usersByEmail[victim.Email] = victim
	repo.usersByID[victim.ID] = victim

	svc := &GoogleOAuthService{repo: repo, cfg: testCfg()}
	user, err := svc.resolveUser(context.Background(),
		auth.GoogleIdentity{Subject: "attacker-sub", Email: victim.Email, EmailVerified: true}, AuthRequestMeta{})
	if !errors.Is(err, ErrGoogleAccountExists) {
		t.Fatalf("create race must refuse with ErrGoogleAccountExists, got user=%v err=%v", user.ID, err)
	}
	if user.ID != uuid.Nil {
		t.Fatalf("no account may be returned on refusal, got %s", user.ID)
	}
	if repo.linkedSub != "" || repo.linkedUserID != "" {
		t.Fatalf("refusal must not link anything: sub=%q user=%q", repo.linkedSub, repo.linkedUserID)
	}
	if _, ok := repo.usersBySub["attacker-sub"]; ok {
		t.Fatal("refusal must not create a sub mapping")
	}
	if victim.GoogleSub != nil {
		t.Fatal("victim account was mutated")
	}
}

// TestResolveUser_CreateRaceResolvesBySubOnly: when the parallel winner was the
// SAME Google identity (same sub), the fallback resolves through that sub and the
// second login succeeds on the very same account — no duplicate, no refusal.
func TestResolveUser_CreateRaceResolvesBySubOnly(t *testing.T) {
	repo := newMockOAuthRepo()
	repo.createErr = errors.New("duplicate key")
	winner := &models.User{Name: "Same", Email: "same@example.com", Role: models.RoleUser}
	winner.ID = uuid.New()
	sub := "same-sub"
	winner.GoogleSub = &sub
	repo.usersBySub[sub] = winner
	repo.usersByEmail[winner.Email] = winner
	repo.usersByID[winner.ID] = winner

	svc := &GoogleOAuthService{repo: repo, cfg: testCfg()}
	user, err := svc.resolveUser(context.Background(),
		auth.GoogleIdentity{Subject: sub, Email: winner.Email, EmailVerified: true}, AuthRequestMeta{})
	if err != nil {
		t.Fatalf("same-identity race must succeed: %v", err)
	}
	if user.ID != winner.ID {
		t.Fatalf("resolved a different account: %s want %s", user.ID, winner.ID)
	}
}

// TestResolveUser_CreateRaceUnrelatedFailurePropagates: a create failure that is
// NOT a lost race (no sub, no email) surfaces as an error, never as a fallback
// account. Fail closed.
func TestResolveUser_CreateRaceUnrelatedFailurePropagates(t *testing.T) {
	repo := newMockOAuthRepo()
	dbDown := errors.New("connection refused")
	repo.createErr = dbDown

	svc := &GoogleOAuthService{repo: repo, cfg: testCfg()}
	user, err := svc.resolveUser(context.Background(),
		auth.GoogleIdentity{Subject: "fresh-sub", Email: "fresh@example.com", EmailVerified: true}, AuthRequestMeta{})
	if !errors.Is(err, dbDown) {
		t.Fatalf("expected the original create error, got user=%v err=%v", user.ID, err)
	}
	if user.ID != uuid.Nil {
		t.Fatalf("no account may be returned, got %s", user.ID)
	}
}

// TestLinkAccount_LostLinkRaceRefusesConsistently: LinkAccount is read-then-write
// too ("is this sub linked?" → write the mapping). When a parallel link for the
// SAME sub commits first, UNIQUE(provider, provider_user_id) rejects this write;
// the loser must receive the same ErrGoogleIdentityTaken decision the pre-check
// would have produced — not a generic failure — and must never keep the identity.
func TestLinkAccount_LostLinkRaceRefusesConsistently(t *testing.T) {
	repo := newMockOAuthRepo()
	caller := &models.User{Name: "Caller", Email: "caller@example.com", Role: models.RoleUser}
	caller.ID = uuid.New()
	repo.usersByID[caller.ID] = caller
	repo.usersByEmail[caller.Email] = caller

	winner := &models.User{Name: "Winner", Email: "winner@example.com", Role: models.RoleUser}
	winner.ID = uuid.New()
	repo.usersByID[winner.ID] = winner

	// Pre-check misses (the winner is not visible yet); the write then fails and
	// the winner's mapping becomes visible — the exact race state.
	const sub = "contested-sub"
	repo.linkErr = errors.New(`duplicate key value violates unique constraint "idx_ext_ident_provider_user"`)
	repo.linkRaceWinner = winner

	svc := &GoogleOAuthService{repo: repo, cfg: testCfg()}
	user, err := svc.LinkAccount(context.Background(), caller.ID.String(),
		auth.GoogleIdentity{Subject: sub, Email: "g@example.com", EmailVerified: true}, AuthRequestMeta{})
	if !errors.Is(err, ErrGoogleIdentityTaken) {
		t.Fatalf("lost link race: got user=%v err=%v, want ErrGoogleIdentityTaken", user.ID, err)
	}
	if user.ID != uuid.Nil {
		t.Fatalf("refused link must return no user, got %s", user.ID)
	}
	if caller.GoogleSub != nil {
		t.Fatal("caller must not keep the contested identity")
	}
	if repo.usersBySub[sub].ID != winner.ID {
		t.Fatalf("identity resolved to the wrong account: %s", repo.usersBySub[sub].ID)
	}
}

// TestLinkAccount_LostRaceToSameAccountIsIdempotent: if the parallel winner was
// THIS account (user double-clicked "Link Google"), the loser reports success
// without a second write — same decision the pre-check makes on a re-link.
func TestLinkAccount_LostRaceToSameAccountIsIdempotent(t *testing.T) {
	repo := newMockOAuthRepo()
	caller := &models.User{Name: "Caller", Email: "caller@example.com", Role: models.RoleUser}
	caller.ID = uuid.New()
	repo.usersByID[caller.ID] = caller

	const sub = "self-sub"
	repo.linkErr = errors.New("duplicate key")
	repo.linkRaceWinner = caller

	svc := &GoogleOAuthService{repo: repo, cfg: testCfg()}
	user, err := svc.LinkAccount(context.Background(), caller.ID.String(),
		auth.GoogleIdentity{Subject: sub, Email: "g@example.com", EmailVerified: true}, AuthRequestMeta{})
	if err != nil {
		t.Fatalf("self re-link race must succeed: %v", err)
	}
	if user.ID != caller.ID || user.GoogleSub == nil || *user.GoogleSub != sub {
		t.Fatalf("unexpected link result: id=%s sub=%v", user.ID, user.GoogleSub)
	}
}

// TestLinkAccount_UnrelatedWriteFailurePropagates: a link failure with no visible
// sub owner is an infrastructure error, not a decision — surface it.
func TestLinkAccount_UnrelatedWriteFailurePropagates(t *testing.T) {
	repo := newMockOAuthRepo()
	caller := &models.User{Name: "Caller", Email: "caller@example.com", Role: models.RoleUser}
	caller.ID = uuid.New()
	repo.usersByID[caller.ID] = caller

	dbDown := errors.New("connection refused")
	repo.linkErr = dbDown

	svc := &GoogleOAuthService{repo: repo, cfg: testCfg()}
	if _, err := svc.LinkAccount(context.Background(), caller.ID.String(),
		auth.GoogleIdentity{Subject: "any-sub", Email: "g@example.com", EmailVerified: true}, AuthRequestMeta{}); !errors.Is(err, dbDown) {
		t.Fatalf("expected the original link error, got %v", err)
	}
}

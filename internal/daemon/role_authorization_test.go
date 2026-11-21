package daemon

import (
	"context"
	"errors"
	"testing"
)

func TestAuthorizeRoleRestrictsRoleAndScope(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	ctx := context.Background()

	handoff, err := fixture.store.RequestGoalHandoff(ctx, "role-gate-goal", fixture.claimedGoalID, fixture.requesterID, "delegate")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := fixture.store.ReceiveGoalHandoff(ctx, handoff.ID, fixture.claimedGoalID, fixture.receiverID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	d := New(fixture.store)
	if err := d.authorizeRole(ctx, []string{"subcommander"}, 0, fixture.claimedGoalID, 0, fixture.receiverID, "test"); err != nil {
		t.Fatalf("subcommander authorization: %v", err)
	}
	if err := d.authorizeRole(ctx, []string{"commander"}, 0, fixture.claimedGoalID, 0, fixture.receiverID, "test"); !errors.Is(err, ErrRoleUnauthorized) {
		t.Fatalf("subcommander as commander error = %v, want ErrRoleUnauthorized", err)
	}
	if err := d.authorizeRole(ctx, []string{"subcommander"}, 0, fixture.unclaimedGoalID, 0, fixture.receiverID, "test"); !errors.Is(err, ErrRoleUnauthorized) {
		t.Fatalf("subcommander outside goal error = %v, want ErrRoleUnauthorized", err)
	}
}

func TestAuthorizeRoleDevelopmentModePermitsOnlyImmediateLowerRole(t *testing.T) {
	fixture := newTaskHandoffRPCTestFixture(t)
	ctx := context.Background()
	d := New(fixture.store)

	if err := d.authorizeRole(ctx, []string{"executor"}, 0, 0, fixture.claimedTaskID, fixture.requesterID, "test"); !errors.Is(err, ErrRoleUnauthorized) {
		t.Fatalf("ordinary subcommander as executor error = %v, want ErrRoleUnauthorized", err)
	}
	if err := fixture.store.EnableDevelopmentMode(ctx, fixture.requesterID); err != nil {
		t.Fatalf("EnableDevelopmentMode: %v", err)
	}
	if err := d.authorizeRole(ctx, []string{"executor"}, 0, 0, fixture.claimedTaskID, fixture.requesterID, "test"); err != nil {
		t.Fatalf("development subcommander as executor: %v", err)
	}
	if err := d.authorizeRole(ctx, []string{"commander"}, 0, 0, fixture.claimedTaskID, fixture.requesterID, "test"); !errors.Is(err, ErrRoleUnauthorized) {
		t.Fatalf("development subcommander as commander error = %v, want ErrRoleUnauthorized", err)
	}
}

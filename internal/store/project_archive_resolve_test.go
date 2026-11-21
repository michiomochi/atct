package store

import (
	"context"
	"errors"
	"testing"
)

func TestResolveProjectReturnsArchivedProjectOnExactMatch(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	project, err := s.CreateProject(ctx, "archived", "/home/user")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := s.ArchiveProject(ctx, project.ID); err != nil {
		t.Fatalf("ArchiveProject: %v", err)
	}

	got, err := s.ResolveProject(ctx, "/home/user")
	if err != nil {
		t.Fatalf("ResolveProject exact archived root: %v", err)
	}
	if got.ID != project.ID {
		t.Fatalf("ResolveProject ID = %d, want %d", got.ID, project.ID)
	}
	if got.ArchivedAt == nil {
		t.Fatal("ResolveProject ArchivedAt = nil, want archived project so the archived error path applies")
	}
}

func TestResolveProjectDoesNotCaptureSubdirectoryOfArchivedParent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	parent, err := s.CreateProject(ctx, "home", "/home/user")
	if err != nil {
		t.Fatalf("CreateProject parent: %v", err)
	}
	if _, err := s.ArchiveProject(ctx, parent.ID); err != nil {
		t.Fatalf("ArchiveProject parent: %v", err)
	}

	_, err = s.ResolveProject(ctx, "/home/user/work/app")
	if !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("ResolveProject under archived parent err = %v, want ErrProjectNotFound", err)
	}
}

func TestResolveProjectPrefersActiveChildOverArchivedParent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	parent, err := s.CreateProject(ctx, "home", "/home/user")
	if err != nil {
		t.Fatalf("CreateProject parent: %v", err)
	}
	if _, err := s.ArchiveProject(ctx, parent.ID); err != nil {
		t.Fatalf("ArchiveProject parent: %v", err)
	}
	child, err := s.CreateProject(ctx, "app", "/home/user/work/app")
	if err != nil {
		t.Fatalf("CreateProject child: %v", err)
	}

	got, err := s.ResolveProject(ctx, "/home/user/work/app/internal")
	if err != nil {
		t.Fatalf("ResolveProject under active child: %v", err)
	}
	if got.ID != child.ID {
		t.Fatalf("ResolveProject ID = %d (%s), want active child %d", got.ID, got.Name, child.ID)
	}
	if got.ArchivedAt != nil {
		t.Fatalf("ResolveProject returned archived project %q", got.Name)
	}
}

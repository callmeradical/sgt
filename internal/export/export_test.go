package export

import (
	"context"
	"testing"
	"time"
)

type mockTarget struct {
	lastRecord Record
}

func (m *mockTarget) Export(ctx context.Context, rec Record) error {
	m.lastRecord = rec
	return nil
}

func TestTargetInterface(t *testing.T) {
	var target Target = &mockTarget{}

	now := time.Now()
	rec := Record{
		Kind:      "intent",
		ID:        "123",
		Project:   "test-project",
		Repo:      "test-repo",
		Position:  1,
		Status:    "open",
		Statement: "test statement",
		CreatedAt: now,
		UpdatedAt: now,
	}

	err := target.Export(context.Background(), rec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mock, ok := target.(*mockTarget)
	if !ok {
		t.Fatalf("expected mockTarget")
	}

	if mock.lastRecord.ID != "123" {
		t.Errorf("expected ID 123, got %s", mock.lastRecord.ID)
	}
	if mock.lastRecord.Project != "test-project" {
		t.Errorf("expected Project test-project, got %s", mock.lastRecord.Project)
	}
}

func TestRecord(t *testing.T) {
	now := time.Now()
	rec := Record{
		Kind:      "bullet",
		ID:        "456",
		Project:   "proj",
		Repo:      "",
		Position:  -1,
		Status:    "done",
		Statement: "",
		CreatedAt: now,
		UpdatedAt: now,
	}

	if rec.Kind != "bullet" {
		t.Errorf("expected Kind bullet, got %s", rec.Kind)
	}
	if rec.ID != "456" {
		t.Errorf("expected ID 456, got %s", rec.ID)
	}
	if rec.Project != "proj" {
		t.Errorf("expected Project proj, got %s", rec.Project)
	}
	if rec.Repo != "" {
		t.Errorf("expected empty Repo, got %s", rec.Repo)
	}
	if rec.Position != -1 {
		t.Errorf("expected Position -1, got %d", rec.Position)
	}
	if rec.Status != "done" {
		t.Errorf("expected Status done, got %s", rec.Status)
	}
	if rec.Statement != "" {
		t.Errorf("expected empty Statement, got %s", rec.Statement)
	}
	if rec.CreatedAt != now {
		t.Errorf("expected CreatedAt %v, got %v", now, rec.CreatedAt)
	}
	if rec.UpdatedAt != now {
		t.Errorf("expected UpdatedAt %v, got %v", now, rec.UpdatedAt)
	}
}

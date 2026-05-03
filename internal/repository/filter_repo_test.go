package repository

import (
	"context"
	"errors"
	"strings"
	"testing"

	oas "github.com/mikaelstaldal/mymail/internal/api"
)

func makeFilter(action oas.FilterAction, matchFrom, matchTo, matchSubject string) oas.Filter {
	return oas.Filter{
		Name:         "test",
		MatchFrom:    matchFrom,
		MatchTo:      matchTo,
		MatchSubject: matchSubject,
		Action:       action,
		Stop:         true,
	}
}

func TestCreateFilter_ValidationErrors(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))
	noPos := oas.OptInt{}

	cases := []struct {
		name   string
		filter oas.Filter
		want   error
	}{
		{
			name:   "all match fields empty",
			filter: makeFilter(oas.FilterActionMarkRead, "", "", ""),
			want:   ErrInvalidFilter,
		},
		{
			name:   "all match fields whitespace-only",
			filter: makeFilter(oas.FilterActionMarkRead, "  ", "\t", " "),
			want:   ErrInvalidFilter,
		},
		{
			name:   "invalid action",
			filter: makeFilter(oas.FilterAction("invalid"), "from@example.com", "", ""),
			want:   ErrInvalidAction,
		},
		{
			name: "move without folder_id",
			filter: func() oas.Filter {
				f := makeFilter(oas.FilterActionMove, "from@example.com", "", "")
				// FolderID left as zero value (not set)
				return f
			}(),
			want: ErrInvalidFolderTarget,
		},
		{
			name: "move with null folder_id",
			filter: func() oas.Filter {
				f := makeFilter(oas.FilterActionMove, "from@example.com", "", "")
				f.FolderID.SetToNull()
				return f
			}(),
			want: ErrInvalidFolderTarget,
		},
		{
			name: "move to Sent (2) — forbidden",
			filter: func() oas.Filter {
				f := makeFilter(oas.FilterActionMove, "from@example.com", "", "")
				f.FolderID = oas.NewOptNilInt(2)
				return f
			}(),
			want: ErrInvalidFolderTarget,
		},
		{
			name: "move to Drafts (3) — forbidden",
			filter: func() oas.Filter {
				f := makeFilter(oas.FilterActionMove, "from@example.com", "", "")
				f.FolderID = oas.NewOptNilInt(3)
				return f
			}(),
			want: ErrInvalidFolderTarget,
		},
		{
			name: "move to Scheduled (5) — forbidden",
			filter: func() oas.Filter {
				f := makeFilter(oas.FilterActionMove, "from@example.com", "", "")
				f.FolderID = oas.NewOptNilInt(5)
				return f
			}(),
			want: ErrInvalidFolderTarget,
		},
		{
			name: "move to Snoozed (6) — forbidden",
			filter: func() oas.Filter {
				f := makeFilter(oas.FilterActionMove, "from@example.com", "", "")
				f.FolderID = oas.NewOptNilInt(6)
				return f
			}(),
			want: ErrInvalidFolderTarget,
		},
		{
			name: "move to id 50 — not a valid target",
			filter: func() oas.Filter {
				f := makeFilter(oas.FilterActionMove, "from@example.com", "", "")
				f.FolderID = oas.NewOptNilInt(50)
				return f
			}(),
			want: ErrInvalidFolderTarget,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.CreateFilter(ctx, tc.filter, noPos)
			if !errors.Is(err, tc.want) {
				t.Errorf("CreateFilter: got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCreateFilter_ValidMoveTargets(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewFilterRepository(db)
	// Seed a user folder.
	db.Exec(`INSERT INTO folders(id,name,slug,position) VALUES(100,'Archive','archive',10)`)
	noPos := oas.OptInt{}

	validTargets := []int{1, 4, 7, 100}
	for _, target := range validTargets {
		f := makeFilter(oas.FilterActionMove, "from@example.com", "", "")
		f.FolderID = oas.NewOptNilInt(target)
		got, err := r.CreateFilter(ctx, f, noPos)
		if err != nil {
			t.Errorf("CreateFilter(folder_id=%d): unexpected error %v", target, err)
			continue
		}
		if fid, ok := got.FolderID.Get(); !ok || fid != target {
			t.Errorf("folder_id roundtrip: got %v, want %d", got.FolderID, target)
		}
	}
}

func TestCreateFilter_PositionAutoAssign(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))
	noPos := oas.OptInt{}

	f1, err := r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "", "", "newsletter"), noPos)
	if err != nil {
		t.Fatalf("first CreateFilter: %v", err)
	}
	if f1.Position != 0 {
		t.Errorf("first filter position = %d, want 0", f1.Position)
	}

	f2, err := r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "", "", "spam"), noPos)
	if err != nil {
		t.Fatalf("second CreateFilter: %v", err)
	}
	if f2.Position != 1 {
		t.Errorf("second filter position = %d, want 1", f2.Position)
	}
}

func TestCreateFilter_ExplicitPositionZero(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))

	// Create a filter at position 5 first so the table is non-empty.
	pos5 := oas.OptInt{}
	pos5.SetTo(5)
	if _, err := r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "", "", "first"), pos5); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Explicitly request position 0 — must NOT trigger auto-assign.
	pos0 := oas.OptInt{}
	pos0.SetTo(0)
	got, err := r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "", "", "second"), pos0)
	if err != nil {
		t.Fatalf("CreateFilter(pos=0): %v", err)
	}
	if got.Position != 0 {
		t.Errorf("position = %d, want 0 (explicit)", got.Position)
	}
}

func TestGetFilter_NotFound(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))

	_, err := r.GetFilter(ctx, 9999)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetFilter: got %v, want ErrNotFound", err)
	}
}

func TestListFilters_OrderedByPositionThenID(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))

	pos := func(n int) oas.OptInt { o := oas.OptInt{}; o.SetTo(n); return o }

	r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "b@x", "", ""), pos(2))
	r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "a@x", "", ""), pos(1))
	r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "c@x", "", ""), pos(1))

	filters, err := r.ListFilters(ctx)
	if err != nil {
		t.Fatalf("ListFilters: %v", err)
	}
	if len(filters) != 3 {
		t.Fatalf("len = %d, want 3", len(filters))
	}
	// position 1 (two entries) before position 2; within same position, lower id first.
	if filters[0].MatchFrom != "a@x" || filters[1].MatchFrom != "c@x" || filters[2].MatchFrom != "b@x" {
		t.Errorf("order wrong: %v %v %v", filters[0].MatchFrom, filters[1].MatchFrom, filters[2].MatchFrom)
	}
}

func TestUpdateFilter_ValidationErrors(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))

	// Seed a valid filter to update.
	noPos := oas.OptInt{}
	orig, err := r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "from@x", "", ""), noPos)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	cases := []struct {
		name   string
		filter oas.Filter
		want   error
	}{
		{
			name:   "all match fields empty",
			filter: makeFilter(oas.FilterActionDrop, "", "", ""),
			want:   ErrInvalidFilter,
		},
		{
			name:   "invalid action",
			filter: makeFilter(oas.FilterAction("bad"), "from@x", "", ""),
			want:   ErrInvalidAction,
		},
		{
			name: "move without folder_id",
			filter: makeFilter(oas.FilterActionMove, "from@x", "", ""),
			want:   ErrInvalidFolderTarget,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.UpdateFilter(ctx, int64(orig.ID), tc.filter)
			if !errors.Is(err, tc.want) {
				t.Errorf("UpdateFilter: got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestUpdateFilter_NotFound(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))

	_, err := r.UpdateFilter(ctx, 9999, makeFilter(oas.FilterActionDrop, "from@x", "", ""))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateFilter: got %v, want ErrNotFound", err)
	}
}

func TestUpdateFilter_Roundtrip(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))

	noPos := oas.OptInt{}
	orig, err := r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "old@x", "", ""), noPos)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	updated := makeFilter(oas.FilterActionMarkRead, "", "new@x", "")
	updated.Position = 5
	updated.Stop = false
	got, err := r.UpdateFilter(ctx, int64(orig.ID), updated)
	if err != nil {
		t.Fatalf("UpdateFilter: %v", err)
	}
	if got.MatchFrom != "" || got.MatchTo != "new@x" || got.Action != oas.FilterActionMarkRead || got.Stop || got.Position != 5 {
		t.Errorf("unexpected result: %+v", got)
	}
}

func TestDeleteFilter(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))

	noPos := oas.OptInt{}
	f, err := r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "from@x", "", ""), noPos)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := r.DeleteFilter(ctx, int64(f.ID)); err != nil {
		t.Fatalf("DeleteFilter: %v", err)
	}
	if err := r.DeleteFilter(ctx, int64(f.ID)); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: got %v, want ErrNotFound", err)
	}
}

func TestReorderFilters(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))
	noPos := oas.OptInt{}

	f1, _ := r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "a@x", "", ""), noPos)
	f2, _ := r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "b@x", "", ""), noPos)
	f3, _ := r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "c@x", "", ""), noPos)

	id1, id2, id3 := int64(f1.ID), int64(f2.ID), int64(f3.ID)

	t.Run("duplicate id", func(t *testing.T) {
		_, err := r.ReorderFilters(ctx, []int64{id1, id1, id3})
		if !errors.Is(err, ErrDuplicateID) {
			t.Errorf("got %v, want ErrDuplicateID", err)
		}
	})

	t.Run("unknown id", func(t *testing.T) {
		_, err := r.ReorderFilters(ctx, []int64{id1, id2, 9999})
		if !errors.Is(err, ErrUnknownID) {
			t.Errorf("got %v, want ErrUnknownID", err)
		}
	})

	t.Run("incomplete — missing one", func(t *testing.T) {
		_, err := r.ReorderFilters(ctx, []int64{id1, id2})
		if !errors.Is(err, ErrIncompleteReorder) {
			t.Errorf("got %v, want ErrIncompleteReorder", err)
		}
	})

	t.Run("valid reorder", func(t *testing.T) {
		n, err := r.ReorderFilters(ctx, []int64{id3, id1, id2})
		if err != nil {
			t.Fatalf("ReorderFilters: %v", err)
		}
		if n != 3 {
			t.Errorf("rows updated = %d, want 3", n)
		}
		filters, _ := r.ListFilters(ctx)
		if filters[0].ID != f3.ID || filters[1].ID != f1.ID || filters[2].ID != f2.ID {
			t.Errorf("wrong order after reorder: %v %v %v", filters[0].ID, filters[1].ID, filters[2].ID)
		}
	})
}

func TestReorderFilters_EmptyIsNoop(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))

	n, err := r.ReorderFilters(ctx, []int64{})
	if err != nil {
		t.Fatalf("ReorderFilters on empty table: %v", err)
	}
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
}

func TestReorderFilters_EmptyIDsOnNonEmptyTable(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))
	noPos := oas.OptInt{}

	r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "a@x", "", ""), noPos)

	_, err := r.ReorderFilters(ctx, []int64{})
	if !errors.Is(err, ErrIncompleteReorder) {
		t.Errorf("got %v, want ErrIncompleteReorder", err)
	}
}

func TestScanFilter_FolderIDNullability(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))
	noPos := oas.OptInt{}

	// Non-move filter: folder_id should be stored as NULL.
	f, err := r.CreateFilter(ctx, makeFilter(oas.FilterActionMarkRead, "", "to@x", ""), noPos)
	if err != nil {
		t.Fatalf("CreateFilter: %v", err)
	}
	got, _ := r.GetFilter(ctx, int64(f.ID))
	if !got.FolderID.Null {
		t.Errorf("expected FolderID to be null for non-move filter, got %v", got.FolderID)
	}
}

func TestCreateFilter_StopField(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))
	noPos := oas.OptInt{}

	f := makeFilter(oas.FilterActionDrop, "from@x", "", "")
	f.Stop = false
	got, err := r.CreateFilter(ctx, f, noPos)
	if err != nil {
		t.Fatalf("CreateFilter: %v", err)
	}
	if got.Stop {
		t.Error("Stop should be false")
	}
}

func TestCreateFilter_AllMatchFieldCombinations(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))
	noPos := oas.OptInt{}

	// Each individual field being non-empty should be sufficient.
	cases := []oas.Filter{
		makeFilter(oas.FilterActionDrop, "from@x", "", ""),
		makeFilter(oas.FilterActionDrop, "", "to@x", ""),
		makeFilter(oas.FilterActionDrop, "", "", "subject"),
	}
	for _, f := range cases {
		if _, err := r.CreateFilter(ctx, f, noPos); err != nil {
			t.Errorf("CreateFilter(%q,%q,%q): unexpected error %v", f.MatchFrom, f.MatchTo, f.MatchSubject, err)
		}
	}
}

func TestFilterNonMoveActionDoesNotRequireFolderID(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))
	noPos := oas.OptInt{}

	for _, action := range []oas.FilterAction{oas.FilterActionTrash, oas.FilterActionMarkRead, oas.FilterActionDrop} {
		f := makeFilter(action, "from@x", "", "")
		// FolderID intentionally not set.
		if _, err := r.CreateFilter(ctx, f, noPos); err != nil {
			t.Errorf("action=%s: unexpected error %v", action, err)
		}
	}
}

func TestReorderFilters_LargeIDsNotInTable(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))
	noPos := oas.OptInt{}

	f, _ := r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "from@x", "", ""), noPos)
	// Pass a valid ID plus an unknown large ID.
	_, err := r.ReorderFilters(ctx, []int64{int64(f.ID), 99999})
	if !errors.Is(err, ErrUnknownID) {
		t.Errorf("got %v, want ErrUnknownID", err)
	}
}

func TestCreateFilter_MatchSubjectTrimCheck(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))
	noPos := oas.OptInt{}

	// A match_subject of just spaces must fail even if non-empty string.
	f := makeFilter(oas.FilterActionDrop, "", "", strings.Repeat(" ", 10))
	_, err := r.CreateFilter(ctx, f, noPos)
	if !errors.Is(err, ErrInvalidFilter) {
		t.Errorf("whitespace-only match_subject: got %v, want ErrInvalidFilter", err)
	}
}

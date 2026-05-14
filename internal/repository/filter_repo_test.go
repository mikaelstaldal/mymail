package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
			assert.ErrorIs(t, err, tc.want)
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
		assert.NoError(t, err, "CreateFilter(folder_id=%d)", target)
		if err == nil {
			fid, ok := got.FolderID.Get()
			assert.True(t, ok)
			assert.Equal(t, target, fid)
		}
	}
}

func TestCreateFilter_PositionAutoAssign(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))
	noPos := oas.OptInt{}

	f1, err := r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "", "", "newsletter"), noPos)
	require.NoError(t, err)
	assert.Equal(t, 0, f1.Position)

	f2, err := r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "", "", "spam"), noPos)
	require.NoError(t, err)
	assert.Equal(t, 1, f2.Position)
}

func TestCreateFilter_ExplicitPositionZero(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))

	// Create a filter at position 5 first so the table is non-empty.
	pos5 := oas.OptInt{}
	pos5.SetTo(5)
	_, err := r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "", "", "first"), pos5)
	require.NoError(t, err)

	// Explicitly request position 0 — must NOT trigger auto-assign.
	pos0 := oas.OptInt{}
	pos0.SetTo(0)
	got, err := r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "", "", "second"), pos0)
	require.NoError(t, err)
	assert.Equal(t, 0, got.Position, "position should be 0 (explicit)")
}

func TestGetFilter_NotFound(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))

	_, err := r.GetFilter(ctx, 9999)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestListFilters_OrderedByPositionThenID(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))

	pos := func(n int) oas.OptInt { o := oas.OptInt{}; o.SetTo(n); return o }

	r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "b@x", "", ""), pos(2))
	r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "a@x", "", ""), pos(1))
	r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "c@x", "", ""), pos(1))

	filters, err := r.ListFilters(ctx)
	require.NoError(t, err)
	require.Len(t, filters, 3)

	// position 1 (two entries) before position 2; within same position, lower id first.
	assert.Equal(t, "a@x", filters[0].MatchFrom)
	assert.Equal(t, "c@x", filters[1].MatchFrom)
	assert.Equal(t, "b@x", filters[2].MatchFrom)
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
			name:   "move without folder_id",
			filter: makeFilter(oas.FilterActionMove, "from@x", "", ""),
			want:   ErrInvalidFolderTarget,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.UpdateFilter(ctx, int64(orig.ID), tc.filter)
			assert.ErrorIs(t, err, tc.want)
		})
	}
}

func TestUpdateFilter_NotFound(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))

	_, err := r.UpdateFilter(ctx, 9999, makeFilter(oas.FilterActionDrop, "from@x", "", ""))
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestUpdateFilter_Roundtrip(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))

	noPos := oas.OptInt{}
	orig, err := r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "old@x", "", ""), noPos)
	require.NoError(t, err)

	updated := makeFilter(oas.FilterActionMarkRead, "", "new@x", "")
	updated.Position = 5
	updated.Stop = false
	got, err := r.UpdateFilter(ctx, int64(orig.ID), updated)
	require.NoError(t, err)

	assert.Empty(t, got.MatchFrom)
	assert.Equal(t, "new@x", got.MatchTo)
	assert.Equal(t, oas.FilterActionMarkRead, got.Action)
	assert.False(t, got.Stop)
	assert.Equal(t, 5, got.Position)
}

func TestDeleteFilter(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))

	noPos := oas.OptInt{}
	f, err := r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "from@x", "", ""), noPos)
	require.NoError(t, err)

	err = r.DeleteFilter(ctx, int64(f.ID))
	assert.NoError(t, err)

	err = r.DeleteFilter(ctx, int64(f.ID))
	assert.ErrorIs(t, err, ErrNotFound)
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
		assert.ErrorIs(t, err, ErrDuplicateID)
	})

	t.Run("unknown id", func(t *testing.T) {
		_, err := r.ReorderFilters(ctx, []int64{id1, id2, 9999})
		assert.ErrorIs(t, err, ErrUnknownID)
	})

	t.Run("incomplete — missing one", func(t *testing.T) {
		_, err := r.ReorderFilters(ctx, []int64{id1, id2})
		assert.ErrorIs(t, err, ErrIncompleteReorder)
	})

	t.Run("valid reorder", func(t *testing.T) {
		n, err := r.ReorderFilters(ctx, []int64{id3, id1, id2})
		require.NoError(t, err)
		assert.Equal(t, 3, n)

		filters, err := r.ListFilters(ctx)
		require.NoError(t, err)
		assert.Equal(t, f3.ID, filters[0].ID)
		assert.Equal(t, f1.ID, filters[1].ID)
		assert.Equal(t, f2.ID, filters[2].ID)
	})
}

func TestReorderFilters_EmptyIsNoop(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))

	n, err := r.ReorderFilters(ctx, []int64{})
	assert.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestReorderFilters_EmptyIDsOnNonEmptyTable(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))
	noPos := oas.OptInt{}

	r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "a@x", "", ""), noPos)

	_, err := r.ReorderFilters(ctx, []int64{})
	assert.ErrorIs(t, err, ErrIncompleteReorder)
}

func TestScanFilter_FolderIDNullability(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))
	noPos := oas.OptInt{}

	// Non-move filter: folder_id should be stored as NULL.
	f, err := r.CreateFilter(ctx, makeFilter(oas.FilterActionMarkRead, "", "to@x", ""), noPos)
	require.NoError(t, err)

	got, err := r.GetFilter(ctx, int64(f.ID))
	require.NoError(t, err)
	assert.True(t, got.FolderID.Null)
}

func TestCreateFilter_StopField(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))
	noPos := oas.OptInt{}

	f := makeFilter(oas.FilterActionDrop, "from@x", "", "")
	f.Stop = false
	got, err := r.CreateFilter(ctx, f, noPos)
	require.NoError(t, err)
	assert.False(t, got.Stop)
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
		_, err := r.CreateFilter(ctx, f, noPos)
		assert.NoError(t, err, "CreateFilter(%q,%q,%q)", f.MatchFrom, f.MatchTo, f.MatchSubject)
	}
}

func TestFilterNonMoveActionDoesNotRequireFolderID(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))
	noPos := oas.OptInt{}

	for _, action := range []oas.FilterAction{oas.FilterActionTrash, oas.FilterActionMarkRead, oas.FilterActionDrop} {
		f := makeFilter(action, "from@x", "", "")
		// FolderID intentionally not set.
		_, err := r.CreateFilter(ctx, f, noPos)
		assert.NoError(t, err, "action=%s", action)
	}
}

func TestReorderFilters_LargeIDsNotInTable(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))
	noPos := oas.OptInt{}

	f, _ := r.CreateFilter(ctx, makeFilter(oas.FilterActionDrop, "from@x", "", ""), noPos)
	// Pass a valid ID plus an unknown large ID.
	_, err := r.ReorderFilters(ctx, []int64{int64(f.ID), 99999})
	assert.ErrorIs(t, err, ErrUnknownID)
}

func TestCreateFilter_MatchSubjectTrimCheck(t *testing.T) {
	ctx := context.Background()
	r := NewFilterRepository(openTestDB(t))
	noPos := oas.OptInt{}

	// A match_subject of just spaces must fail even if non-empty string.
	f := makeFilter(oas.FilterActionDrop, "", "", strings.Repeat(" ", 10))
	_, err := r.CreateFilter(ctx, f, noPos)
	assert.ErrorIs(t, err, ErrInvalidFilter)
}

package ui

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>
#include <stdint.h>
#include <stdlib.h>

// atlasRanks is the sort order the table's sorters read. Go computes it once a
// tick and writes it straight into r; the comparison below is C all the way, so
// a re-sort costs no calls back into Go.
typedef struct {
	int32_t *r;
	size_t n;
} atlasRanks;

static GQuark atlas_idx_quark(void) {
	static GQuark q;
	if (!q) q = g_quark_from_static_string("atlas-row-index");
	return q;
}

// A row that carries no index yet (its object was only just added to the model)
// sorts after every row that does, and equal to the others like it. That is
// still one consistent order, so GTK never sees a broken contract.
static int atlas_rank_cmp(gconstpointer a, gconstpointer b, gpointer data) {
	atlasRanks *t = data;
	GQuark q = atlas_idx_quark();
	size_t ia = GPOINTER_TO_SIZE(g_object_get_qdata((GObject *)a, q));
	size_t ib = GPOINTER_TO_SIZE(g_object_get_qdata((GObject *)b, q));
	int ua = ia == 0 || ia > t->n, ub = ib == 0 || ib > t->n;
	if (ua || ub) return ua - ub;
	int32_t ra = t->r[ia - 1], rb = t->r[ib - 1];
	return ra < rb ? -1 : ra > rb;
}

static GtkSorter *atlas_rank_sorter_new(atlasRanks *t) {
	return GTK_SORTER(gtk_custom_sorter_new(atlas_rank_cmp, t, NULL));
}

static atlasRanks *atlas_ranks_new(void) { return calloc(1, sizeof(atlasRanks)); }

static int atlas_ranks_reserve(atlasRanks *t, size_t n) {
	if (n <= t->n) return 1;
	size_t cap = t->n ? t->n : 256;
	while (cap < n) cap *= 2;
	int32_t *p = realloc(t->r, cap * sizeof(int32_t));
	if (!p) return 0;
	t->r = p;
	t->n = cap;
	return 1;
}

static size_t atlas_index(uintptr_t obj) {
	return GPOINTER_TO_SIZE(g_object_get_qdata((GObject *)obj, atlas_idx_quark()));
}

// atlas_store_append adds n plain GObjects to the store, item i carrying the
// index first+i. A GListStore hands its items out without calling back into Go,
// which the gioutil model this replaces did for every fetch.
static void atlas_store_append(uintptr_t store, size_t first, size_t n) {
	GObject **items = g_new(GObject *, n);
	for (size_t i = 0; i < n; i++) {
		items[i] = g_object_new(G_TYPE_OBJECT, NULL);
		g_object_set_qdata(items[i], atlas_idx_quark(), GSIZE_TO_POINTER(first + i + 1));
	}
	GListStore *ls = (GListStore *)store;
	g_list_store_splice(ls, g_list_model_get_n_items(G_LIST_MODEL(ls)), 0, (gpointer *)items, n);
	for (size_t i = 0; i < n; i++) g_object_unref(items[i]);
	g_free(items);
}
*/
import "C"

import (
	"slices"
	"unsafe"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-monitor/internal/process"
)

// rankTable holds the order the table is sorted in, where C can read it.
//
// The sort used to be a GtkCustomSorter calling a Go function for every
// comparison: thousands of calls across cgo a second, on a table that re-sorts
// every tick. Now each row carries its index in appsView.order once (as object
// data), Go ranks the rows by the sorted column each tick and writes the ranks
// into a C array, and the sorters are C functions comparing two ranks.
type rankTable struct {
	c    *C.atlasRanks
	perm []int32
}

func newRankTable() *rankTable {
	return &rankTable{c: C.atlas_ranks_new()}
}

// newSorter makes a sorter over the table's ranks. Every column uses the same
// ranks; only the column being sorted on has them computed, and GTK inverts the
// result itself for a descending sort.
func (t *rankTable) newSorter() *gtk.Sorter {
	obj := coreglib.AssumeOwnership(unsafe.Pointer(C.atlas_rank_sorter_new(t.c)))
	return &gtk.Sorter{Object: obj}
}

// set ranks rows by less and stores the result for the sorters.
func (t *rankTable) set(rows []*procRow, less func(a, b *process.Proc) bool) {
	if len(rows) == 0 {
		return
	}
	if C.atlas_ranks_reserve(t.c, C.size_t(len(rows))) == 0 {
		return
	}
	ranks := unsafe.Slice((*int32)(unsafe.Pointer(t.c.r)), len(rows))
	t.perm = rankRows(rows, less, t.perm, ranks)
}

// rowModel is the table's base list model: a GListStore of plain GObjects, each
// carrying only its row's index in rows (as object data, the same tag the C
// sorter reads). Nothing in it calls back into Go, so a sort or filter pass,
// which asks the model for every item, stays in C. rows is parallel to the store
// and only grows: retired rows are recycled, never removed.
type rowModel struct {
	store *gio.ListStore
	rows  []*procRow
}

func newRowModel() *rowModel {
	return &rowModel{store: gio.NewListStore(coreglib.TypeObject)}
}

func (m *rowModel) Len() int { return len(m.rows) }

func (m *rowModel) At(i int) *procRow { return m.rows[i] }

// Append adds rows at the end of the model.
func (m *rowModel) Append(rows ...*procRow) {
	if len(rows) == 0 {
		return
	}
	first := len(m.rows)
	m.rows = append(m.rows, rows...)
	C.atlas_store_append(C.uintptr_t(m.store.Native()), C.size_t(first), C.size_t(len(rows)))
}

// row maps one of the model's items back to its row, or nil.
func (m *rowModel) row(obj *coreglib.Object) *procRow {
	if obj == nil {
		return nil
	}
	idx := int(C.atlas_index(C.uintptr_t(obj.Native())))
	if idx == 0 || idx > len(m.rows) {
		return nil
	}
	return m.rows[idx-1]
}

// rankRows writes into ranks[i] the position row i takes when the rows are
// sorted ascending by less on their keys. Rows that read the same share a rank,
// so the sorter calls them equal and GTK leaves equal rows in the order they
// already had: a table sorted by CPU does not shuffle its idle rows every
// second, or reorder them when a process comes and goes. perm is scratch,
// returned for reuse.
func rankRows(rows []*procRow, less func(a, b *process.Proc) bool, perm, ranks []int32) []int32 {
	perm = perm[:0]
	for i := range rows {
		perm = append(perm, int32(i))
	}
	slices.SortFunc(perm, func(a, b int32) int {
		ka, kb := &rows[a].key, &rows[b].key
		switch {
		case less(ka, kb):
			return -1
		case less(kb, ka):
			return 1
		}
		return int(a) - int(b) // only to make the sort itself deterministic
	})
	var rank int32
	for k, i := range perm {
		if k > 0 && less(&rows[perm[k-1]].key, &rows[i].key) {
			rank++
		}
		ranks[i] = rank
	}
	return perm
}

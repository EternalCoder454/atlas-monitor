package ui

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>

// atlas_icon_texture renders an application icon once, at the size and scale it
// will be shown at, into a texture in the software renderer's own pixel format.
// NULL when there is no such icon, or when it is symbolic: a symbolic icon is
// coloured from the widget showing it, which a texture made here cannot know.
static GdkTexture *atlas_icon_texture(GtkWidget *w, const char *name, int size, int scale) {
	GdkDisplay *display = gtk_widget_get_display(w);
	GtkIconTheme *theme = gtk_icon_theme_get_for_display(display);
	GtkIconPaintable *icon;
	if (name[0] == '/') {
		// Looked up by file, a missing one would come back as the "missing
		// image" picture, and that would be kept in its place.
		if (!g_file_test(name, G_FILE_TEST_IS_REGULAR))
			return NULL;
		GFile *file = g_file_new_for_path(name);
		GIcon *gicon = g_file_icon_new(file);
		icon = gtk_icon_theme_lookup_by_gicon(theme, gicon, size, scale, gtk_widget_get_direction(w), 0);
		g_object_unref(gicon);
		g_object_unref(file);
	} else {
		if (!gtk_icon_theme_has_icon(theme, name))
			return NULL;
		icon = gtk_icon_theme_lookup_icon(theme, name, NULL, size, scale, gtk_widget_get_direction(w), 0);
	}
	if (icon == NULL)
		return NULL;
	// Deprecated in 4.22 without a replacement that older GTKs have; still right.
#pragma GCC diagnostic push
#pragma GCC diagnostic ignored "-Wdeprecated-declarations"
	gboolean symbolic = gtk_icon_paintable_is_symbolic(icon);
#pragma GCC diagnostic pop
	if (symbolic) {
		g_object_unref(icon);
		return NULL;
	}

	// Made once, on the main thread, which is the only one that calls this.
	static GskRenderer *renderer = NULL;
	static gboolean failed = FALSE;
	if (failed) {
		g_object_unref(icon);
		return NULL;
	}
	if (renderer == NULL) {
		renderer = gsk_cairo_renderer_new();
		if (!gsk_renderer_realize_for_display(renderer, display, NULL)) {
			g_clear_object(&renderer);
			failed = TRUE;
			g_object_unref(icon);
			return NULL;
		}
	}

	GtkSnapshot *snapshot = gtk_snapshot_new();
	gtk_snapshot_scale(snapshot, scale, scale);
	gdk_paintable_snapshot(GDK_PAINTABLE(icon), snapshot, size, size);
	g_object_unref(icon);
	GskRenderNode *node = gtk_snapshot_free_to_node(snapshot);
	if (node == NULL)
		return NULL;
	graphene_rect_t viewport = GRAPHENE_RECT_INIT(0, 0, size * scale, size * scale);
	GdkTexture *texture = gsk_renderer_render_texture(renderer, node, &viewport);
	gsk_render_node_unref(node);
	return texture;
}
*/
import "C"

import (
	"unsafe"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// Application icons, made once each and shown from then on as they are.
//
// An icon as the icon theme loads it is in a pixel format the software renderer
// cannot draw from directly, so every frame converted every icon on screen
// again, handing each one to a pool of threads to do it. On Wayland every frame
// redraws the whole window (see "The frame" in assets/style.css), so on the Apps
// page that was a dozen conversions a second, a few per cent of the page's CPU,
// and up to thirty threads kept waiting for the next batch. Drawn once into the
// renderer's own format, an icon needs no conversion at all.

type iconKey struct {
	name        string
	size, scale int
}

// iconTextures holds a texture per icon, size and scale, and nil for an icon it
// could not make one for, so that is not tried again every tick. It is emptied
// whenever the icon theme changes, which is also when an icon that was missing
// may have arrived: an application installed while Atlas runs.
var iconTextures = map[iconKey]gdk.Paintabler{}
var iconThemeWatched bool

// iconShown is what each image that has had a texture was last asked to show,
// so it can be made again when the image's scale changes: when it is first put
// in a window, or the window moves to a screen with another scale.
var iconShown = map[uintptr]string{}

// iconTexture is the ready-made texture for an icon, or nil to fall back to
// showing it by name.
func iconTexture(img *gtk.Image, name string, size int) gdk.Paintabler {
	if !iconThemeWatched {
		iconThemeWatched = true
		if d := gdk.DisplayGetDefault(); d != nil {
			gtk.IconThemeGetForDisplay(d).ConnectChanged(func() { clear(iconTextures) })
		}
	}
	n := img.Object.Native()
	if _, seen := iconShown[n]; !seen {
		img.NotifyProperty("scale-factor", func() {
			if ic := iconShown[n]; ic != "" {
				setIcon(img, ic)
			}
		})
		img.ConnectDestroy(func() { delete(iconShown, n) })
	}
	iconShown[n] = name
	scale := img.ScaleFactor()
	if scale < 1 {
		scale = 1
	}
	key := iconKey{name, size, scale}
	if t, ok := iconTextures[key]; ok {
		return t
	}
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	ptr := C.atlas_icon_texture((*C.GtkWidget)(unsafe.Pointer(img.Object.Native())), cname, C.int(size), C.int(scale))
	var tex gdk.Paintabler
	if ptr != nil {
		// Cast builds gotk4's own wrapper for the texture's actual type.
		tex, _ = coreglib.AssumeOwnership(unsafe.Pointer(ptr)).Cast().(gdk.Paintabler)
	}
	iconTextures[key] = tex
	return tex
}

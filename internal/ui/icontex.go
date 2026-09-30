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

	static GskRenderer *renderer = NULL;
	if (renderer == NULL) {
		renderer = gsk_cairo_renderer_new();
		if (!gsk_renderer_realize_for_display(renderer, display, NULL)) {
			g_clear_object(&renderer);
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

var iconTextures = map[iconKey]*gdk.Texture{}
var iconThemeWatched bool

// iconTexture is the ready-made texture for an icon, or nil to fall back to
// showing it by name.
func iconTexture(img *gtk.Image, name string, size int) *gdk.Texture {
	if !iconThemeWatched {
		iconThemeWatched = true
		// A new icon theme means new pictures for the same names.
		if st := gtk.SettingsGetDefault(); st != nil {
			st.NotifyProperty("gtk-icon-theme-name", func() { clear(iconTextures) })
		}
	}
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
	var tex *gdk.Texture
	if ptr != nil {
		obj := coreglib.AssumeOwnership(unsafe.Pointer(ptr))
		tex = &gdk.Texture{Object: obj}
	}
	iconTextures[key] = tex // a miss is remembered too, so it is not tried every tick
	return tex
}

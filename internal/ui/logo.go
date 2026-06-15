package ui

import (
	"math"

	"github.com/diamondburned/gotk4/pkg/cairo"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// Atlas mark colours (the app's purple accent, plus the pale "world").
var (
	logoR, logoG, logoB    = 0.66, 0.36, 0.86
	worldR, worldG, worldB = 0.97, 0.96, 0.99
)

// drawAtlasLogo paints the Assistant's mark: a rounded triangle (Atlas) bearing
// a small sphere at its apex (the world he shoulders). Drawn with Cairo so it
// needs no asset and stays crisp at any size.
func drawAtlasLogo(_ *gtk.DrawingArea, cr *cairo.Context, w, h int) {
	fw, fh := float64(w), float64(h)
	s := math.Min(fw, fh)
	if s <= 0 {
		return
	}
	cx := fw / 2
	pad := s * 0.18
	topY := pad + s*0.10 // leave headroom above the apex for the sphere
	botY := fh - pad
	half := s*0.5 - pad

	// Triangle. Filling and then stroking the same path with a round line join
	// gives the corners a soft radius.
	cr.MoveTo(cx, topY)
	cr.LineTo(cx+half, botY)
	cr.LineTo(cx-half, botY)
	cr.ClosePath()
	cr.SetSourceRGBA(logoR, logoG, logoB, 1)
	cr.FillPreserve()
	cr.SetLineJoin(cairo.LineJoinRound)
	cr.SetLineWidth(s * 0.11)
	cr.Stroke()

	// Sphere resting on the apex.
	cr.SetSourceRGBA(worldR, worldG, worldB, 0.96)
	cr.Arc(cx, topY, s*0.085, 0, 2*math.Pi)
	cr.Fill()
}

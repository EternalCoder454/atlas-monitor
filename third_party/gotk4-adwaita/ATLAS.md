# gotk4-adwaita, as Atlas builds it

Upstream github.com/diamondburned/gotk4-adwaita/pkg@v0.0.0-20260808200908-d4aecaa0ff32, with its generated signal trampolines returning instead
of panicking when a closure has already been released, and built against gotk4
v0.4.1. Produced by scripts/vendor-adwaita.sh; see that script for why, and
for when this directory can go.

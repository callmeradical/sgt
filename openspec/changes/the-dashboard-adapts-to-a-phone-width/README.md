Makes the one existing dashboard (`internal/ui/static/index.html`) usable
at a phone's viewport width, at the specific places that break today: the
"Pipeline runs" master rail is completely hidden below `lg` (1024px) with
no substitute or toggle to reveal it; icon-only buttons rely on native
`title=` hover tooltips that a touch screen cannot trigger; the workflow
graph's lanes are dense enough to be cramped at narrow widths. No new
page, no new backend endpoint, no new frontend framework — CSS/markup/JS
changes to the existing dashboard only.

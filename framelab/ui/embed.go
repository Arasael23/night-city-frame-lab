// Package ui holds the Frame Lab window (HTML/CSS/JS), embedded into the exe.
package ui

import "embed"

//go:embed index.html style.css app.js uPlot.iife.min.js uPlot.min.css icon.svg fonts/*.woff2
var FS embed.FS

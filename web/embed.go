//go:generate sh -c "cd .. && go run web/bundle.go"

// Package web provides embedded web assets for the HTML report.
package web

import _ "embed"

//go:embed index.html
var IndexHTML string

//go:embed styles.css
var StylesCSS string

//go:embed app.bundle.js
var AppBundleJS string

//go:embed uplot.min.js
var UplotJS string

//go:embed fzstd.min.js
var FzstdJS string

//go:embed report.tmpl
var ReportTmpl string

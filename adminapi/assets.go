package adminapi

import _ "embed"

//go:embed web/index.html
var index string

//go:embed web/app.js
var script string

//go:embed web/style.css
var style string

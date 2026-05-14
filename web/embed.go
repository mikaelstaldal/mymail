package web

import "embed"

//go:embed static/index.html static/app.css
//go:embed static/*.js
//go:embed static/api/*.js
//go:embed static/components/*.js
//go:embed static/layout/*.js
//go:embed static/util/*.js
//go:embed static/views/*.js
//go:embed static/views/settings/*.js
//go:embed static/vendor/preact/preact.module.js
//go:embed static/vendor/preact/hooks.module.js
//go:embed static/vendor/preact/jsx-runtime.module.js
//go:embed static/vendor/quill/quill.js
//go:embed static/vendor/quill/quill.snow.css
var Static embed.FS

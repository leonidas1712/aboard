// Package templates embeds the board templates shipped with Aboard. Each template is an
// aboard.yaml board file named after the template.
package templates

import "embed"

// FS holds every template as <name>.yaml.
//
//go:embed *.yaml
var FS embed.FS

//go:build tools

package tools

import (
	// tfplugindocs generates provider documentation for the Terraform Registry.
	_ "github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs"
)

# Terraform Provider for Pangolin

The **Pangolin** Terraform provider lets you manage [Pangolin](https://github.com/fosrl/pangolin) infrastructure as code — organizations, sites, HTTP resources, targets, private site resources, roles, users, API keys, OLM clients, domains, and all access-control assignments.

[![Terraform Registry](https://img.shields.io/badge/Terraform-Registry-purple)](https://registry.terraform.io/providers/stackopshq/pangolin)
[![OpenTofu Registry](https://img.shields.io/badge/OpenTofu-Registry-yellow)](https://search.opentofu.org/provider/stackopshq/pangolin)
[![License: MPL-2.0](https://img.shields.io/badge/License-MPL--2.0-blue)](LICENSE)

## Requirements

- [Terraform](https://www.terraform.io/) >= 1.0 or [OpenTofu](https://opentofu.org/) >= 1.6
- A Pangolin self-hosted instance with the [Integration API](https://docs.pangolin.net/self-host/advanced/integration-api) enabled
- Go >= 1.21 (only for building from source)

## Usage

```hcl
terraform {
  required_providers {
    pangolin = {
      source  = "stackopshq/pangolin"
      version = "~> 1.0"
    }
  }
}

provider "pangolin" {
  url     = "https://pangolin.example.com"
  api_key = var.pangolin_api_key
  org_id  = "my-org"
}
```

All provider arguments can be set via environment variables:

```bash
export PANGOLIN_URL="https://pangolin.example.com"
export PANGOLIN_API_KEY="your-api-key"
export PANGOLIN_ORG_ID="your-org-id"
```

## Resources

| Resource | Description |
|----------|-------------|
| `pangolin_org` | Organization |
| `pangolin_site` | Site (Newt tunnel connector) |
| `pangolin_resource` | Public HTTP resource (reverse proxy endpoint) |
| `pangolin_target` | Backend target for an HTTP resource |
| `pangolin_site_resource` | Private site resource (VPN-accessible endpoint) |
| `pangolin_role` | Role |
| `pangolin_user` | User |
| `pangolin_domain` | Custom domain |
| `pangolin_api_key` | API key |
| `pangolin_client` | OLM client device |
| `pangolin_resource_role` | Assign a role to an HTTP resource |
| `pangolin_resource_user` | Assign a user to an HTTP resource |
| `pangolin_resource_whitelist` | Add an email to an HTTP resource whitelist |
| `pangolin_site_resource_role` | Assign a role to a private site resource |
| `pangolin_site_resource_user` | Assign a user to a private site resource |
| `pangolin_site_resource_client` | Assign an OLM client to a private site resource |

## Data Sources

| Data Source | Description |
|-------------|-------------|
| `pangolin_domains` | List domains |
| `pangolin_roles` | List roles |
| `pangolin_users` | List users |
| `pangolin_sites` | List sites |
| `pangolin_resources` | List HTTP resources |
| `pangolin_site_resources` | List private site resources |
| `pangolin_api_keys` | List API keys |

## Example

```hcl
# Create a site
resource "pangolin_site" "homelab" {
  name = "homelab"
}

# Expose an app publicly
data "pangolin_domains" "all" {}

resource "pangolin_resource" "app" {
  name      = "my-app"
  subdomain = "app"
  domain_id = data.pangolin_domains.all.domains[0].domain_id
}

resource "pangolin_target" "app" {
  resource_id = pangolin_resource.app.id
  site_id     = pangolin_site.homelab.id
  ip          = "localhost"
  port        = 8080
}

# Restrict access to a role
resource "pangolin_role" "devs" {
  name = "developers"
}

resource "pangolin_resource_role" "app_devs" {
  resource_id = pangolin_resource.app.id
  role_id     = pangolin_role.devs.id
}

# Expose a private service via VPN
resource "pangolin_site_resource" "db" {
  site_id        = pangolin_site.homelab.id
  name           = "postgres"
  mode           = "host"
  destination    = "db.internal"
  alias          = "db.local"
  tcp_port_range = "5432"
}
```

Full documentation is available on the [Terraform Registry](https://registry.terraform.io/providers/stackopshq/pangolin/latest/docs).

## Development

```bash
# Build
go build -o terraform-provider-pangolin

# Run tests
go test ./...

# Regenerate docs
tfplugindocs generate --provider-name pangolin

# Install locally for testing
mkdir -p ~/.terraform.d/plugins/registry.terraform.io/stackopshq/pangolin/1.0.0/$(go env GOOS)_$(go env GOARCH)
cp terraform-provider-pangolin ~/.terraform.d/plugins/registry.terraform.io/stackopshq/pangolin/1.0.0/$(go env GOOS)_$(go env GOARCH)/
```

## License

[Mozilla Public License 2.0](LICENSE)

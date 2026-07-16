# Terraform Provider for Pangolin

The **Pangolin** Terraform provider lets you manage [Pangolin](https://github.com/fosrl/pangolin) infrastructure as code: organizations, sites, HTTP and private site resources, targets, roles, users, API keys, OLM clients, domains, identity providers, access-control rules, and all assignments.

[![Terraform Registry](https://img.shields.io/badge/Terraform-Registry-purple)](https://registry.terraform.io/providers/stackopshq/pangolin)
[![OpenTofu Registry](https://img.shields.io/badge/OpenTofu-Registry-yellow)](https://search.opentofu.org/provider/stackopshq/pangolin)
[![License: MPL-2.0](https://img.shields.io/badge/License-MPL--2.0-blue)](LICENSE)

## Requirements

- [Terraform](https://www.terraform.io/) >= 1.0 or [OpenTofu](https://opentofu.org/) >= 1.6
- A Pangolin self-hosted instance with the [Integration API](https://docs.pangolin.net/self-host/advanced/integration-api) enabled
- Go >= 1.25 (only for building from source, required by `terraform-plugin-framework`)

## Usage

```hcl
terraform {
  required_providers {
    pangolin = {
      source  = "stackopshq/pangolin"
      version = "~> 1.5"
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
| `pangolin_resource` | HTTP resource (public reverse proxy endpoint) |
| `pangolin_target` | Backend target for an HTTP resource |
| `pangolin_site_resource` | Private site resource (TCP/UDP/SSH via VPN) |
| `pangolin_role` | Role |
| `pangolin_user` | User |
| `pangolin_domain` | Custom domain |
| `pangolin_api_key` | API key |
| `pangolin_api_key_actions` | Set of actions (permissions) granted to an API key |
| `pangolin_client` | OLM client device |
| `pangolin_invitation` | Pending organization invitation |
| `pangolin_resource_role` | Assign a role to an HTTP resource |
| `pangolin_resource_user` | Assign a user to an HTTP resource |
| `pangolin_resource_whitelist` | Add an email to an HTTP resource whitelist |
| `pangolin_resource_rule` | Access-control rule for a resource (CIDR, IP, path, country, ASN) |
| `pangolin_resource_password` | Password protection for a resource |
| `pangolin_resource_pincode` | PIN-code protection for a resource |
| `pangolin_resource_header_auth` | Header-based authentication for a resource |
| `pangolin_resource_access_token` | Bearer access token bound to an HTTP resource |
| `pangolin_site_resource_role` | Assign a role to a private site resource |
| `pangolin_site_resource_user` | Assign a user to a private site resource |
| `pangolin_site_resource_client` | Assign an OLM client to a private site resource |
| `pangolin_role_user` | Assign a user to a role |
| `pangolin_user_role` | Additional (cumulative) role binding on a user |
| `pangolin_idp` | OIDC Identity Provider (global) |
| `pangolin_idp_org` | IDP-to-organization policy mapping |
| `pangolin_org_idp` | OIDC IDP scoped to a single organization (single-resource alternative to `pangolin_idp` + `pangolin_idp_org`) |

## Data Sources

| Data Source | Description |
|-------------|-------------|
| `pangolin_domains` | List all domains |
| `pangolin_domain` | Single domain lookup by ID |
| `pangolin_domain_dns_records` | DNS records expected (or verified) for a domain |
| `pangolin_sites` | List all sites |
| `pangolin_site` | Single site lookup by nice ID (full live payload) |
| `pangolin_resources` | List all HTTP resources |
| `pangolin_resource_targets` | List backend targets of a resource (with health-check + routing detail) |
| `pangolin_resource_roles` | List roles granted access to a resource |
| `pangolin_site_resources` | List all private site resources |
| `pangolin_roles` | List all roles |
| `pangolin_users` | List all users |
| `pangolin_user` | Single user lookup by username + idp_id |
| `pangolin_user_by_id` | Single user lookup by user_id (root-only; exposes `server_admin`, `email_verified`, `two_factor_setup_requested`) |
| `pangolin_user_devices` | List user-bound devices for an org (filter by agent, status, online, query; sort by bandwidth) |
| `pangolin_api_keys` | List all API keys |
| `pangolin_access_tokens` | List all resource access tokens in the organization |
| `pangolin_orgs` | List all organizations visible to the calling key (root-only) |
| `pangolin_idps` | List all Identity Providers |
| `pangolin_request_logs` | Query the request audit log |
| `pangolin_logs_analytics` | Aggregate analytics rollup (per-country, per-day, totals) |
| `pangolin_blueprints` | List the blueprint (declarative apply) audit records for the org |
| `pangolin_blueprint` | Single blueprint lookup by ID (exposes raw `contents` + apply outcome) |

## Example

```hcl
# Create a site (Newt tunnel connector)
resource "pangolin_site" "homelab" {
  name = "homelab"
  type = "newt"
}

# Expose an app publicly via HTTP reverse proxy
data "pangolin_domains" "all" {}

resource "pangolin_resource" "app" {
  name      = "my-app"
  subdomain = "app"
  domain_id = data.pangolin_domains.all.domains[0].domain_id
  mode      = "http"
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

# Expose a TCP service via the VPN (private site resource)
resource "pangolin_site_resource" "postgres" {
  site_id        = pangolin_site.homelab.id
  name           = "postgres"
  mode           = "host"
  destination    = "db.internal"
  alias          = "db.local"
  tcp_port_range = "5432"
}

# SSH bastion via PAM (Pangolin 1.19+)
resource "pangolin_site_resource" "ssh_bastion" {
  site_id        = pangolin_site.homelab.id
  name           = "ssh-bastion"
  mode           = "host"
  destination    = "bastion.internal"
  alias          = "bastion.local"
  tcp_port_range = "22"
  pam_mode       = "push"
}
```

Full documentation is available on the [Terraform Registry](https://registry.terraform.io/providers/stackopshq/pangolin/latest/docs).

## Development

```bash
# Build
go build -o terraform-provider-pangolin

# Run tests
go test ./...

# Regenerate docs (idempotent)
go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate --provider-name pangolin

# Install locally for testing
mkdir -p ~/.terraform.d/plugins/registry.terraform.io/stackopshq/pangolin/1.5.0/$(go env GOOS)_$(go env GOARCH)
cp terraform-provider-pangolin ~/.terraform.d/plugins/registry.terraform.io/stackopshq/pangolin/1.5.0/$(go env GOOS)_$(go env GOARCH)/
```

## License

[Mozilla Public License 2.0](LICENSE)

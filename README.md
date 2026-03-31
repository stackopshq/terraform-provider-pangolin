# Terraform Provider for Pangolin

A Terraform/OpenTofu provider for managing [Pangolin](https://github.com/fosrl/pangolin) resources — sites, HTTP resources, targets, private site resources, and access control.

## Requirements

- [OpenTofu](https://opentofu.org/) >= 1.6 or [Terraform](https://www.terraform.io/) >= 1.0
- Go >= 1.21 (for building from source)
- A Pangolin instance with the [Integration API](https://docs.pangolin.net/self-host/advanced/integration-api) enabled

## Installation

### From the Terraform Registry (coming soon)

```hcl
terraform {
  required_providers {
    pangolin = {
      source  = "stackopshq/pangolin"
      version = "~> 0.1"
    }
  }
}
```

### Local development

```bash
go build -o terraform-provider-pangolin
mkdir -p ~/.terraform.d/plugins/registry.terraform.io/stackopshq/pangolin/0.1.0/$(go env GOOS)_$(go env GOARCH)
cp terraform-provider-pangolin ~/.terraform.d/plugins/registry.terraform.io/stackopshq/pangolin/0.1.0/$(go env GOOS)_$(go env GOARCH)/
```

## Configuration

```hcl
provider "pangolin" {
  url     = "https://api.example.com"
  api_key = var.pangolin_api_key
  org_id  = "your-org-id"
}
```

Or via environment variables:

```bash
export PANGOLIN_URL="https://api.example.com"
export PANGOLIN_API_KEY="your-api-key"
export PANGOLIN_ORG_ID="your-org-id"
```

## Resources

### `pangolin_site`

Manages a Pangolin site (tunnel connector).

```hcl
resource "pangolin_site" "homelab" {
  name = "homelab"
}
```

### `pangolin_resource`

Manages a public HTTP resource (reverse proxy endpoint).

```hcl
data "pangolin_domains" "all" {}

resource "pangolin_resource" "app" {
  name      = "My App"
  subdomain = "app"
  domain_id = data.pangolin_domains.all.domains[0].domain_id
  protocol  = "tcp"
}
```

### `pangolin_target`

Manages a backend target for an HTTP resource.

```hcl
resource "pangolin_target" "app_backend" {
  resource_id = pangolin_resource.app.id
  site_id     = pangolin_site.homelab.id
  ip          = "localhost"
  port        = 8080
  method      = "http"
}
```

### `pangolin_site_resource`

Manages a private site resource (VPN-accessible endpoint).

```hcl
resource "pangolin_site_resource" "ssh" {
  name           = "SSH Access"
  site_id        = pangolin_site.homelab.id
  mode           = "host"
  destination    = "localhost"
  alias          = "ssh.internal"
  tcp_port_range = "22"
  udp_port_range = ""
}
```

## Data Sources

### `pangolin_domains`

Retrieves the list of domains for the organization.

```hcl
data "pangolin_domains" "all" {}

output "domains" {
  value = data.pangolin_domains.all.domains
}
```

## Development

```bash
# Build
go build -o terraform-provider-pangolin

# Test
go test ./...

# Install locally
make install
```

## License

MPL-2.0

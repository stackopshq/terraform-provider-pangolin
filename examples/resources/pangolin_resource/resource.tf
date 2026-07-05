data "pangolin_domains" "all" {}

# ---------------------------------------------------------------------
# HTTP resource — the historical Pangolin default. Reverse-proxied by
# the Pangolin edge and gated on a subdomain of a managed domain.
# ---------------------------------------------------------------------
resource "pangolin_resource" "web" {
  name      = "my-app"
  subdomain = "app"
  domain_id = data.pangolin_domains.all.domains[0].domain_id
  protocol  = "tcp"
  sso       = false # public access, no Pangolin authentication
}

# HTTP resource with 1.19+ options: injected headers, host-header
# override, PROXY-protocol upstream, post-auth landing page.
resource "pangolin_resource" "web_advanced" {
  name      = "internal-app"
  subdomain = "internal"
  domain_id = data.pangolin_domains.all.domains[0].domain_id
  mode      = "http"
  sso       = true

  set_host_header        = "backend.internal"
  enable_proxy           = true
  proxy_protocol         = true
  proxy_protocol_version = 2
  post_auth_path         = "/dashboard"

  headers = [
    { name = "X-Forwarded-User", value = "$user" },
    { name = "X-Env", value = "prod" },
  ]
}

# ---------------------------------------------------------------------
# SSH jumpbox — L4 mode, exposed on a fixed proxy port on the Pangolin
# edge. `pam_mode = "push"` triggers a push-notification MFA flow.
# ---------------------------------------------------------------------
resource "pangolin_resource" "ssh_jumpbox" {
  name       = "ssh-jumpbox"
  mode       = "ssh"
  proxy_port = 2222
  pam_mode   = "push"
  sso        = true
}

# ---------------------------------------------------------------------
# RDP resource. `mode = "rdp"` is the Windows remote-desktop L4 mode.
# ---------------------------------------------------------------------
resource "pangolin_resource" "rdp_desktop" {
  name       = "win-workstation"
  mode       = "rdp"
  proxy_port = 3389
  sso        = true
}

# ---------------------------------------------------------------------
# Maintenance-mode toggle. Any resource can be flipped into a
# maintenance page without touching the upstream config.
# ---------------------------------------------------------------------
resource "pangolin_resource" "under_maintenance" {
  name      = "docs"
  subdomain = "docs"
  domain_id = data.pangolin_domains.all.domains[0].domain_id
  mode      = "http"

  maintenance_mode_enabled   = true
  maintenance_mode_type      = "planned"
  maintenance_title          = "Docs are getting a facelift"
  maintenance_message        = "We'll be back in ~15 minutes."
  maintenance_estimated_time = "2026-07-05T14:00:00Z"
}

resource "pangolin_org_idp" "authentik" {
  org_id          = "my-org"
  name            = "Authentik"
  client_id       = "pangolin"
  client_secret   = var.oidc_client_secret
  auth_url        = "https://auth.example.com/application/o/authorize/"
  token_url       = "https://auth.example.com/application/o/token/"
  identifier_path = "sub"
  email_path      = "email"
  name_path       = "name"
  scopes          = "openid email profile"
  variant         = "oidc"
  auto_provision  = true

  # Per-org token-claim → role mapping (optional)
  role_mapping = "'Member'"
}

output "oidc_redirect_url" {
  description = "Configure this URL as the OAuth callback in your OIDC provider."
  value       = pangolin_org_idp.authentik.redirect_url
}

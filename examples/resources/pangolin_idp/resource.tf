resource "pangolin_idp" "example" {
  name            = "my-oidc-idp"
  client_id       = "my-client-id"
  client_secret   = var.oidc_client_secret
  auth_url        = "https://auth.example.com/authorize"
  token_url       = "https://auth.example.com/token"
  identifier_path = "sub"
  scopes          = "openid email profile"
  email_path      = "email"
  auto_provision  = true
}

# A Google Workspace-flavored IdP. variant = "google" lets Pangolin pre-fill
# provider-specific defaults and tweak the consent flow.
resource "pangolin_idp" "google" {
  name            = "Google Workspace"
  variant         = "google"
  client_id       = var.google_oidc_client_id
  client_secret   = var.google_oidc_client_secret
  auth_url        = "https://accounts.google.com/o/oauth2/auth"
  token_url       = "https://oauth2.googleapis.com/token"
  identifier_path = "sub"
  scopes          = "openid email profile"
  email_path      = "email"
  auto_provision  = true
}

output "redirect_url" {
  value = pangolin_idp.example.redirect_url
}

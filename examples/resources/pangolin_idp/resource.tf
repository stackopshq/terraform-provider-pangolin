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

output "redirect_url" {
  value = pangolin_idp.example.redirect_url
}

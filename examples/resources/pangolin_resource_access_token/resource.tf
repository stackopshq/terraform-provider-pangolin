resource "pangolin_resource_access_token" "ci_bot" {
  resource_id       = pangolin_resource.filebrowser.id
  title             = "ci-bot"
  description       = "CI/CD pipeline bearer token"
  valid_for_seconds = 7200
}

output "bearer_token" {
  value     = pangolin_resource_access_token.ci_bot.token
  sensitive = true
}

# A token without an explicit lifetime defaults to ~30 days
# (session_length = 2592000000 ms) and expires_at = null
# (the API treats it as never-expiring).
resource "pangolin_resource_access_token" "long_lived" {
  resource_id = pangolin_resource.filebrowser.id
  title       = "long-lived"
}

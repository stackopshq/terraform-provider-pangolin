# Invite a user to the organization. The invitation is created
# immediately; the recipient claims it by opening invite_link, at which
# point Pangolin creates a real user and assigns role_id.
resource "pangolin_invitation" "alice" {
  email       = "alice@example.com"
  role_id     = pangolin_role.developers.id
  valid_hours = 72
  send_email  = false # use invite_link directly instead of email
}

# Hand the invite URL to the recipient via your secret-distribution
# tool of choice (1Password, Bitwarden, etc.). It is single-use.
output "alice_invite_link" {
  value     = pangolin_invitation.alice.invite_link
  sensitive = true
}

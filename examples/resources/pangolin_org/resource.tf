resource "pangolin_org" "example" {
  org_id         = "acme-corp"
  name           = "Acme Corp"
  subnet         = "100.90.0.0/24"
  utility_subnet = "100.96.0.0/24"

  # Enterprise security policies (optional)
  require_two_factor       = true
  max_session_length_hours = 12
  password_expiry_days     = 90

  # Audit log retention (days). 0 disables a stream.
  # The access / action / connection streams require an active
  # enterprise subscription on Pangolin Cloud.
  log_retention_days_request    = 30
  log_retention_days_access     = 90
  log_retention_days_action     = 90
  log_retention_days_connection = 30
}

# The SSH CA keypair is returned by the API and stored in state.
# Distribute the public key to hosts that should trust certificates
# signed by Pangolin for the SSH bastion feature.
output "ssh_ca_public_key" {
  value = pangolin_org.example.ssh_ca_public_key
}

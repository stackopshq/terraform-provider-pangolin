# Single-domain lookup by ID.
data "pangolin_domain" "main" {
  id = "egcq4bwo41tak9o"
}

output "is_verified" {
  value = data.pangolin_domain.main.verified
}

# Surface a remediation hint if the last verification attempt failed.
output "verification_error" {
  value = (
    data.pangolin_domain.main.failed
    ? coalesce(data.pangolin_domain.main.error_message, "(no message)")
    : null
  )
}

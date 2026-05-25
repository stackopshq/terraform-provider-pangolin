# Allow only internal CIDR range
resource "pangolin_resource_rule" "allow_internal" {
  resource_id = pangolin_resource.example.id
  action      = "ACCEPT"
  match       = "CIDR"
  value       = "10.0.0.0/8"
  priority    = 1
  enabled     = true
}

# Block a specific country
resource "pangolin_resource_rule" "block_country" {
  resource_id = pangolin_resource.example.id
  action      = "DROP"
  match       = "COUNTRY"
  value       = "CN"
  priority    = 10
}

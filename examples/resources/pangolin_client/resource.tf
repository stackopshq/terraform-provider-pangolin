resource "pangolin_client" "example" {
  name = "laptop-alice"
}

# Temporarily block a client without deleting it (its secret stays valid).
resource "pangolin_client" "decommissioned" {
  name    = "laptop-bob"
  blocked = true
}

# Archive a retired client to keep audit history.
resource "pangolin_client" "retired" {
  name     = "laptop-old"
  archived = true
}

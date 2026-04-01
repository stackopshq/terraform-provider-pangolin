resource "pangolin_user" "example" {
  email    = "alice@example.com"
  password = var.alice_password
  name     = "Alice"
}

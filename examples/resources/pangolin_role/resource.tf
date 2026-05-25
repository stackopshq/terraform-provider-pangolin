resource "pangolin_role" "developers" {
  name        = "developers"
  description = "Access for the development team"

  # SSH bastion: allow members to open SSH sessions through Pangolin,
  # with restricted sudo and explicit Unix groups on the target host.
  allow_ssh           = true
  ssh_create_home_dir = true
  ssh_sudo_mode       = "restricted"
  ssh_sudo_commands = [
    "/usr/bin/systemctl restart nginx",
    "/usr/bin/journalctl",
  ]
  ssh_unix_groups = ["docker", "kvm"]

  # Require admin approval each time a new device tries to use this role.
  require_device_approval = true
}

# Tighter role: no SSH at all, view-only.
resource "pangolin_role" "auditors" {
  name        = "auditors"
  description = "Read-only access for compliance audits"
  allow_ssh   = false
}

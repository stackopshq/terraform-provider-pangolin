data "pangolin_users" "all" {}

output "user_emails" {
  value = [for u in data.pangolin_users.all.users : u.email]
}

output "user_usernames" {
  value = [for u in data.pangolin_users.all.users : u.username]
}

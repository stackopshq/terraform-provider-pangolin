# Look up a Pangolin user by their username + the IDP that issued
# that username. Usernames are unique only within an IDP, so both
# inputs are required.
data "pangolin_idps" "all" {}

locals {
  authentik_idp_id = one([
    for i in data.pangolin_idps.all.idps : i.id if i.name == "Authentik"
  ])
}

data "pangolin_user" "deploy_bot" {
  username = "deploy-bot"
  idp_id   = local.authentik_idp_id
}

# Surface the role IDs currently bound to the user - useful to feed a
# pangolin_role_user resource that keeps the binding in lock-step.
output "deploy_bot_role_ids" {
  value = [for r in data.pangolin_user.deploy_bot.roles : r.role_id]
}

# Alert if the user does not have 2FA enabled.
output "deploy_bot_2fa_warning" {
  value = (
    data.pangolin_user.deploy_bot.two_factor_enabled
    ? null
    : "${data.pangolin_user.deploy_bot.username} does not have 2FA enabled"
  )
}

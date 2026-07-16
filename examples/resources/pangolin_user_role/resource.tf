# Bind an additional role to an existing user. This is a *cumulative*
# binding - the user keeps any other roles they already have.
#
# For a single-role assignment that strips other bindings, use
# `pangolin_role_user` instead.
data "pangolin_user" "alice" {
  username = "alice"
  idp_id   = 1
}

resource "pangolin_user_role" "alice_admin" {
  user_id = data.pangolin_user.alice.id
  role_id = pangolin_role.admin.id
}

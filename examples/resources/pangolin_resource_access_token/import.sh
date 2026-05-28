# Resource access tokens import by their string identifier. Look it up
# via the pangolin_access_tokens data source.
#
# Note: the `token` (bearer secret) cannot be recovered from the
# Pangolin API after creation. After importing, it is set to empty in
# state. Rotate by tainting and re-applying.
terraform import pangolin_resource_access_token.ci_bot <access_token_id>

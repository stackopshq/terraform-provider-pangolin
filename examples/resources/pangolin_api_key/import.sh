# API keys import by their string identifier. Look it up via the
# pangolin_api_keys data source.
#
# Note: `secret` cannot be recovered from the Pangolin API after
# creation. After importing, it is set to empty in state.
terraform import pangolin_api_key.example <api_key_id>

# New release system: MakefileEc2.mk only holds service start / healthcheck
# commands. Build artifacts are produced by MakefileBuild.mk.
#
# Target naming convention:
#   start-${env_key}-${app_name}
#   healthcheck-${env_key}-${app_name}
#
# geth on EC2 is started by the release system's own launch flow, so no
# start targets are defined here yet. Add them below when needed.

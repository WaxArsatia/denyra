group "default" {
  targets = ["gateway", "pipeline", "lidarr", "navidrome"]
}

variable "DEPENDENCIES_LOCK_SHA256" {
  default = "cd7c701e37586a450e0c16193c5b21571d615651d936a8a95cb8d32d18bdfbf0"
}

target "gateway" {
  context = "."
  dockerfile = "deploy/docker/gateway.Dockerfile"
  tags = ["denyra/acquisition-gateway:local"]
  platforms = ["linux/amd64"]
  labels = {
    "io.denyra.dependencies-lock.sha256" = DEPENDENCIES_LOCK_SHA256
    "io.denyra.target-platform" = "linux/amd64"
  }
}

target "pipeline" {
  context = "."
  dockerfile = "deploy/docker/pipeline.Dockerfile"
  tags = ["denyra/media-pipeline:local"]
  platforms = ["linux/amd64"]
  labels = {
    "io.denyra.dependencies-lock.sha256" = DEPENDENCIES_LOCK_SHA256
    "io.denyra.target-platform" = "linux/amd64"
  }
}

target "lidarr" {
  context = "."
  dockerfile = "deploy/docker/lidarr.Dockerfile"
  tags = ["denyra/lidarr:local"]
  platforms = ["linux/amd64"]
}

target "navidrome" {
  context = "."
  dockerfile = "deploy/docker/navidrome.Dockerfile"
  tags = ["denyra/navidrome:local"]
  platforms = ["linux/amd64"]
}

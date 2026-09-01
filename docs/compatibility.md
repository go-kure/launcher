# Launcher Compatibility Matrix

This document describes the versions of infrastructure tools that Launcher supports.

## Version Philosophy

Launcher maintains two version concepts for each dependency:

1. **Build Version** (`current` in versions.yaml): The exact library version Launcher imports in go.mod
2. **Deployment Compatibility** (`supported_range`): The range of deployed tool versions that Launcher can generate YAML for

## Go Version

**Current:** Go 1.26.6


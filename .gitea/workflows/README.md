# Gitea workflow discovery

Gitea uses the first workflow directory that exists on a branch. Keep this
directory tracked even when it contains only this README. It prevents Gitea from
discovering Helm's GitHub CI and deployment workflows in `.github/workflows`.

Branches that carry the Helm shadow workflow put its YAML in this directory.

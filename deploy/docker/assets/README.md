# Verified build assets

Runtime assets are fetched only by immutable URL plus BuildKit `ADD --checksum`.
Their canonical identities live in `dependencies.lock.json`. No artifact in this
directory may bypass the lock or introduce runtime installation/update behavior.

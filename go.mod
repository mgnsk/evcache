module github.com/mgnsk/evcache/v4

go 1.22

require github.com/mgnsk/ringlist v0.0.0-20231025190742-1f621b925911

retract v0.0.0+incompatible // Contains bugs.
retract v1.0.0+incompatible // Contains bugs.
retract v2.0.0+incompatible // Contains bugs.
retract v3.0.0+incompatible // Contains bugs.
retract v4.0.0 // Uses unsupported Keys() API.
retract v4.0.1 // Uses unsupported Keys() API.

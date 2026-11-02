package artifact

// MaxZipEntrySize exposes the per-entry decompression limit for tests so they
// can lower it without allocating huge buffers.
var MaxZipEntrySize = &maxZipEntrySize

// Package components is the workstation's component-layout and
// install-transaction primitive (polyforge#162, 84-components-A).
//
// The layout it owns is the one the ticket settled: a components root
// (the /components volume in the workstation; any directory for a
// caller) holds every versioned component under releases/<ver>/ — the
// release checkout root (package.json, skills/, workflows/) plus
// bin/pf, pi-web/ and harness/{pi,claude}/ and manifest.json — and the
// root's current and previous symlinks are the only pointers anyone
// consumes. current is the single commit point of an install: a version
// directory is staged under staging/, verified against its manifest's
// digests and published by one rename before current is swapped, so an
// interruption at any point leaves current either on the old complete
// version or on the new complete one — never between them.
//
// The same package writes the image's seed manifest (the build's
// seedtool is its thin front end), so the digests, the component set
// and the manifest shape are defined once: the seed the image carries
// and the volume's installed versions are judged by the same code. The
// first-boot entry (84-components-C) and the in-container upgrade verb
// (#92) call these primitives; they never re-implement version
// comparison, locking or chain switching.
package components

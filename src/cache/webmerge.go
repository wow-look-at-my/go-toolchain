package cache

// MergeWebSummary folds src into dst. Counters add, because each standalone
// cacheprog reports its own slice of one run. IndexKeys is the size of the
// index every process was offered, so it takes the largest value seen rather
// than a sum. IndexAuthoritative holds only while every reporter had an
// authoritative index: one loader that fell back leaves the run without a
// trustworthy view of what the remote holds.
//
// first says whether dst is still the zero accumulator, which is what lets
// IndexAuthoritative start from src instead of from false.
func MergeWebSummary(dst *WebSummary, src WebSummary, first bool) {
	dst.Hits += src.Hits
	dst.Puts += src.Puts

	dst.MissNotInIndex += src.MissNotInIndex
	dst.MissHTTP404 += src.MissHTTP404
	dst.MissHTTPError += src.MissHTTPError
	dst.MissNoOutputID += src.MissNoOutputID
	dst.MissReadBody += src.MissReadBody
	dst.MissDecompress += src.MissDecompress
	dst.MissChecksum += src.MissChecksum
	dst.MissBuildID += src.MissBuildID
	dst.MissModuleIndex += src.MissModuleIndex
	dst.MissNetwork += src.MissNetwork

	dst.SkippedEmptyIndex += src.SkippedEmptyIndex
	dst.SkippedNotInIndex += src.SkippedNotInIndex
	dst.SkippedBatchBackoff += src.SkippedBatchBackoff
	dst.Reclaimed404 += src.Reclaimed404

	dst.PutSkippedKnown += src.PutSkippedKnown
	dst.PutRefusedModIndex += src.PutRefusedModIndex
	dst.PutRefusedBuildID += src.PutRefusedBuildID

	if src.IndexKeys > dst.IndexKeys {
		dst.IndexKeys = src.IndexKeys
	}
	if first {
		dst.IndexAuthoritative = src.IndexAuthoritative
	} else {
		dst.IndexAuthoritative = dst.IndexAuthoritative && src.IndexAuthoritative
	}
}

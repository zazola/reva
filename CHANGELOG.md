Changelog for reva unreleased (UNRELEASED)
=======================================

The following sections list the changes in reva unreleased relevant to
reva users. The changes are ordered by importance.

Summary
-------

 * Fix #2063: Allow absolute path for filename when backing up from stdin
 * Fix #2174: Save files with invalid timestamps
 * Fix #2249: Read fresh metadata for unmodified files

Details
-------

 * Bugfix #2063: Allow absolute path for filename when backing up from stdin

   When backing up from stdin, handle directory path for `--stdin-filename`. This can be used to
   specify the full path for the backed-up file.

   https://github.com/restic/restic/issues/2063

 * Bugfix #2174: Save files with invalid timestamps

   When restic reads invalid timestamps (year is before 0000 or after 9999) it refused to read and
   archive the file. We've changed the behavior and will now save modified timestamps with the
   year set to either 0000 or 9999, the rest of the timestamp stays the same, so the file will be saved
   (albeit with a bogus timestamp).

   https://github.com/restic/restic/issues/2174
   https://github.com/restic/restic/issues/1173

 * Bugfix #2249: Read fresh metadata for unmodified files

   Restic took all metadata for files which were detected as unmodified, not taking into account
   changed metadata (ownership, mode). This is now corrected.

   https://github.com/restic/restic/issues/2249
   https://github.com/restic/restic/pull/2252


Changelog for reva 0.0.1 (2019-05-09)
=======================================

The following sections list the changes in reva 0.0.1 relevant to
reva users. The changes are ordered by importance.

Summary
-------

 * Enh #962: Improve memory and runtime for the s3 backend

Details
-------

 * Enhancement #962: Improve memory and runtime for the s3 backend

   We've updated the library used for accessing s3, switched to using a lower level API and added
   caching for some requests. This lead to a decrease in memory usage and a great speedup. In
   addition, we added benchmark functions for all backends, so we can track improvements over
   time. The Continuous Integration test service we're using (Travis) now runs the s3 backend
   tests not only against a Minio server, but also against the Amazon s3 live service, so we should
   be notified of any regressions much sooner.

   https://github.com/restic/restic/pull/962
   https://github.com/restic/restic/pull/960
   https://github.com/restic/restic/pull/946
   https://github.com/restic/restic/pull/938
   https://github.com/restic/restic/pull/883



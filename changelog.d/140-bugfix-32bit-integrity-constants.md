The provider now compiles for 32-bit targets. The data-integrity job's byte limits
(default and maximum object size) are typed as `int64` instead of relying on the
platform `int`, which is 32 bits on `arm` and `386` and cannot hold 5 GiB. This
only fixes the build: 64-bit users see no behaviour change, and no state or
schema moved.

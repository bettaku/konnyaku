-- Separate database for `go test` (TEST_DATABASE_URL); the tests drop and recreate its schema.
CREATE DATABASE konnyaku_test OWNER konnyaku;

BEGIN;

ALTER TABLE IF EXISTS repositories DROP CONSTRAINT IF EXISTS repositories_primary_is_member_fkey;
DROP TABLE IF EXISTS product_repositories;
DROP TABLE IF EXISTS repositories;
DROP TABLE IF EXISTS products;

COMMIT;

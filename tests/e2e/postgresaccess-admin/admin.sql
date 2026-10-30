DO $$ BEGIN
  IF session_user <> 'e2e-admin@example.com' OR current_user <> session_user THEN
    RAISE EXCEPTION 'expected a genuinely authenticated personal session';
  END IF;
  IF NOT pg_has_role(current_user, 'app', 'USAGE') THEN
    RAISE EXCEPTION 'CNPG did not grant effective database-owner membership';
  END IF;
  IF pg_has_role(current_user, 'pg_monitor', 'MEMBER') OR pg_has_role(current_user, 'pg_maintain', 'MEMBER') THEN
    RAISE EXCEPTION 'database admin unexpectedly received instance maintenance roles';
  END IF;
  IF EXISTS (SELECT FROM pg_roles WHERE rolname = current_user AND
    (rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls)) THEN
    RAISE EXCEPTION 'personal admin unexpectedly has privileged role attributes';
  END IF;
END $$;

CREATE SCHEMA e2e_personal;
CREATE TABLE e2e_personal.identity AS SELECT oid FROM pg_roles WHERE rolname = session_user;
ALTER TABLE public.e2e_app_owned ADD COLUMN migrated boolean;
DROP TABLE public.e2e_app_drop;

DO $$ BEGIN
  IF (SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname = 'e2e_personal') <> session_user THEN
    RAISE EXCEPTION 'new personal schema should be owned by the personal role';
  END IF;
END $$;

SET ROLE app;
CREATE SCHEMA e2e_migration;
CREATE TABLE e2e_migration.app_owned (id integer);
DO $$ BEGIN
  IF session_user <> 'e2e-admin@example.com' OR current_user <> 'app' THEN
    RAISE EXCEPTION 'SET ROLE must retain the personal session identity';
  END IF;
  IF (SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid = 'e2e_migration.app_owned'::regclass) <> 'app' THEN
    RAISE EXCEPTION 'migration objects should be app-owned after SET ROLE';
  END IF;
END $$;
RESET ROLE;

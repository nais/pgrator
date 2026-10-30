DO $$ BEGIN
  IF session_user <> 'e2e-admin@example.com' OR current_user <> session_user THEN
    RAISE EXCEPTION 'downgraded connection lost the personal identity';
  END IF;
  IF (SELECT oid FROM pg_roles WHERE rolname = session_user) <> (SELECT oid FROM e2e_personal.identity) THEN
    RAISE EXCEPTION 'replacement must retain the durable role OID and its objects';
  END IF;
  IF pg_has_role(current_user, 'app', 'MEMBER') THEN
    RAISE EXCEPTION 'CNPG retained database-owner membership after downgrade';
  END IF;
  IF NOT pg_has_role(current_user, 'app_readwritecreate', 'USAGE') THEN
    RAISE EXCEPTION 'replacement readwritecreate membership is not effective';
  END IF;

  BEGIN
    EXECUTE 'CREATE SCHEMA e2e_must_not_create';
    RAISE EXCEPTION 'readwritecreate unexpectedly created a new schema';
  EXCEPTION WHEN insufficient_privilege THEN NULL;
  END;
  BEGIN
    EXECUTE 'ALTER TABLE public.e2e_app_owned ADD COLUMN must_not_add integer';
    RAISE EXCEPTION 'readwritecreate unexpectedly altered an app-owned table';
  EXCEPTION WHEN insufficient_privilege THEN NULL;
  END;
  BEGIN
    EXECUTE 'SET ROLE app';
    RAISE EXCEPTION 'readwritecreate unexpectedly switched to the app owner';
  EXCEPTION WHEN insufficient_privilege THEN NULL;
  END;
END $$;

-- Lower access still permits creating public objects and managing personal ones.
CREATE TABLE public.e2e_create_only (id integer);
DROP TABLE e2e_personal.identity;
DROP SCHEMA e2e_personal;

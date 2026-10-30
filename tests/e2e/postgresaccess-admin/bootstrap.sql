-- Only fixture setup uses CNPG's local superuser connection. All assertions
-- run in independent connections authenticated with the personal credentials.
SET ROLE app;
CREATE TABLE public.e2e_app_owned (id integer);
CREATE TABLE public.e2e_app_drop (id integer);

ALTER TABLE apis ADD COLUMN methods TEXT[];
UPDATE apis SET methods=ARRAY[method];
ALTER TABLE apis ALTER COLUMN methods SET NOT NULL;
ALTER TABLE apis ADD CONSTRAINT apis_methods_valid CHECK (cardinality(methods) BETWEEN 1 AND 7 AND methods <@ ARRAY['GET','POST','PUT','PATCH','DELETE','HEAD','OPTIONS']::TEXT[] AND method=methods[1]);
CREATE INDEX apis_methods_idx ON apis USING GIN(methods);
CREATE TABLE api_routes (api_id TEXT NOT NULL REFERENCES apis(id) ON DELETE CASCADE, method TEXT NOT NULL, path TEXT NOT NULL, PRIMARY KEY(method,path));
INSERT INTO api_routes(api_id,method,path) SELECT id,method,path FROM apis;

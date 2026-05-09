STATIC := static
TSC    := tsc
OAT    := openapi-typescript

.PHONY: all typecheck compile gen-api clean

all: gen-api compile

# Type-check only (no output files)
typecheck:
	$(TSC) --project $(STATIC)/tsconfig.json --noEmit

# Compile TypeScript → .js files alongside .ts sources
compile:
	$(TSC) --project $(STATIC)/tsconfig.json

# Re-generate TypeScript API types from openapi.yaml
gen-api:
	$(OAT) ../openapi.yaml -o $(STATIC)/api/types.ts

clean:
	find $(STATIC) -name '*.js' ! -path '$(STATIC)/vendor/*' -delete

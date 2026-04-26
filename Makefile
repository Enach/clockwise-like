.PHONY: update-frontend db-shell

## Pull latest changes from the Lovable frontend repo into frontend/src/
update-frontend:
	git fetch lovable-frontend
	git subtree pull --prefix=_lovable_tmp lovable-frontend main --squash
	cp -r _lovable_tmp/src/. frontend/src/
	@echo "Merging package.json dependencies..."
	node -e " \
	  const fs = require('fs'); \
	  const base = JSON.parse(fs.readFileSync('frontend/package.json')); \
	  const lovable = JSON.parse(fs.readFileSync('_lovable_tmp/package.json')); \
	  base.dependencies = { ...lovable.dependencies, ...base.dependencies }; \
	  fs.writeFileSync('frontend/package.json', JSON.stringify(base, null, 2) + '\n'); \
	  console.log('Done merging dependencies'); \
	"
	git rm -r _lovable_tmp
	git add frontend/
	git commit -m "chore: sync frontend from Lovable (smart-calendar-flow)"
	@echo "Frontend updated. Run 'docker compose build' to rebuild."

## Open a psql shell into the running postgres container
db-shell:
	docker compose exec postgres psql -U clockwise -d clockwise

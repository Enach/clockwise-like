.PHONY: clone-frontend db-shell install-hooks

## Clone (or update) Enach/smart-calendar-flow as a sibling for local frontend dev
clone-frontend:
	@if [ -d ../smart-calendar-flow ]; then \
		echo "Updating ../smart-calendar-flow"; \
		cd ../smart-calendar-flow && git pull --ff-only; \
	else \
		echo "Cloning into ../smart-calendar-flow"; \
		git clone git@github.com:Enach/smart-calendar-flow.git ../smart-calendar-flow; \
	fi

## Point git at the committed hook scripts
install-hooks:
	git config core.hooksPath .githooks
	@echo "Git hooks installed — pre-commit will run lint + build."

## Open a psql shell into the running postgres container
db-shell:
	docker compose exec postgres psql -U clockwise -d clockwise

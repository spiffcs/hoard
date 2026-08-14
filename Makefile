OWNER = spiffcs
PROJECT = hoard

TOOL_DIR = .tool
BINNY = $(TOOL_DIR)/binny
TASK = $(TOOL_DIR)/task

.DEFAULT_GOAL := help

$(BINNY):
	@mkdir -p $(TOOL_DIR)
	@curl -sSfL https://get.anchore.io/binny | sh -s -- -b $(TOOL_DIR)

.PHONY: task
$(TASK) task: $(BINNY)
	@$(BINNY) install task -q

.PHONY: ci-bootstrap-go
ci-bootstrap-go:
	go mod download

%:
	@make --silent $(TASK)
	@$(TASK) $@

TASKS := $(shell bash -c "test -f $(TASK) && NO_COLOR=1 $(TASK) -l | grep '^\* ' | cut -d' ' -f2 | tr -d ':' | tr '\n' ' '" ) $(shell bash -c "test -f $(TASK) && NO_COLOR=1 $(TASK) -l | grep 'aliases:' | cut -d ':' -f 3 | tr '\n' ' ' | tr -d ','")

.PHONY: $(TASKS)
$(TASKS): $(TASK)
	@$(TASK) $@

help: $(TASK)
	@$(TASK) -l

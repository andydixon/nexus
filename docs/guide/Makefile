# Builds nexus-guide.pdf with LuaLaTeX (three passes: contents, page references, then page overlays settle).
# Needs LuaLaTeX with luaotfload (Debian/Ubuntu: texlive-luatex texlive-latex-extra).
# Screenshots: tools/screenshots.sh (needs xvfb-run, sshd, python3).
OUT ?= build
nexus-guide.pdf: nexus-guide.tex nexus-guide.sty chapters/*.tex img/*
	mkdir -p $(OUT)
	lualatex -interaction=nonstopmode -halt-on-error -output-directory=$(OUT) nexus-guide.tex > /dev/null || (grep -A8 '^!' $(OUT)/nexus-guide.log; false)
	for i in 1 2; do lualatex -interaction=nonstopmode -halt-on-error -output-directory=$(OUT) nexus-guide.tex > /dev/null; done
	cp $(OUT)/nexus-guide.pdf .
clean:
	rm -rf $(OUT)

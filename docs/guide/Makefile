# Builds nexus-guide.pdf with LuaLaTeX (three passes so the contents and page references settle).
# Needs LuaLaTeX and the Inter and Fira fonts from TeX Live (Debian/Ubuntu: texlive-luatex texlive-latex-extra
# texlive-fonts-extra texlive-pictures). Layout: the uncomplex. user-guide format, see nexus-guide.sty.
# Screenshots: tools/screenshots.sh (needs xvfb-run, sshd, python3).
OUT ?= build
nexus-guide.pdf: nexus-guide.tex nexus-guide.sty chapters/*.tex img/*
	mkdir -p $(OUT)
	lualatex -interaction=nonstopmode -halt-on-error -output-directory=$(OUT) nexus-guide.tex > /dev/null || (grep -A8 '^!' $(OUT)/nexus-guide.log; false)
	for i in 1 2; do lualatex -interaction=nonstopmode -halt-on-error -output-directory=$(OUT) nexus-guide.tex > /dev/null; done
	cp $(OUT)/nexus-guide.pdf .
clean:
	rm -rf $(OUT)

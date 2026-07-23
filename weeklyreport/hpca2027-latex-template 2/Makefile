PAPER = main

TEX := $(wildcard *.tex) $(wildcard sections/*.tex)
BIB = refs.bib
FIGURES := $(wildcard Figure/*.png)

.PHONY: all clean

all: $(PAPER).pdf

$(PAPER).pdf: $(TEX) $(BIB) $(FIGURES)
	 pdflatex $(PAPER)
	@if grep -q '\\bibdata' $(PAPER).aux; then bibtex $(PAPER); fi
	pdflatex $(PAPER)
	pdflatex $(PAPER)
	rm -f $(PAPER).aux $(PAPER).bbl $(PAPER).blg $(PAPER).log

clean:
	rm -f *.dvi $(PAPER).ps *.aux *.bbl *.blg *.log *.out $(PAPER).pdf

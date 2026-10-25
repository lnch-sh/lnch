const buttons = document.querySelectorAll<HTMLButtonElement>("[data-copy]");

for (const button of buttons) {
  button.addEventListener("click", async () => {
    const value = button.dataset.copy ?? "";
    const previous = button.textContent ?? "copy";
    try {
      await navigator.clipboard.writeText(value);
      button.textContent = "copied";
    } catch {
      const pre = button.previousElementSibling;
      if (pre instanceof HTMLElement) {
        const range = document.createRange();
        range.selectNodeContents(pre);
        const selection = window.getSelection();
        selection?.removeAllRanges();
        selection?.addRange(range);
      }
      button.textContent = "selected";
    }
    window.setTimeout(() => {
      button.textContent = previous;
    }, 1400);
  });
}

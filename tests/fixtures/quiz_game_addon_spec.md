# Quiz Game – Add-On Spec (Go stdlib only)

## Goals
- Web quiz with radio buttons & submit button.
- Loads questions from `questions.json` (repo root).
- Randomly selects **NumQuestions** per run (default **3**).
- +1 per correct answer; no penalty for wrong; no skips.
- Show **running score after each question**.
- **20s timer** per question (constant).
- End screen: show raw score + percentage, ask for **player name**, store in a **local leaderboard** (JSON file). Offer **Play Again** (new random set).
- No CLI; keep existing endpoints and flat layout/style.

---

## Project Structure (additions in bold)
```
helloworld/
├── go.mod
├── main.go
├── home.html
├── main_test.go
├── Makefile
├── **quiz.html**            # per-question page
├── **results.html**         # end screen with name entry
├── **leaderboard.html**     # top scores
├── **questions.json**       # 10+ astrology trivia (devs to author)
├── **leaderboard.json**     # persisted highscores (created at runtime)
└── **quiz_test.go**         # unit/handler tests for quiz layer
```
Keep Go **1.24.3** and the existing `/` and `/health` behavior intact.

---

## Constants (in `main.go`)
```go
const (
    NumQuestions        = 3          // number of questions per run
    QuestionTimerSecs   = 20         // per-question countdown
    MaxLeaderboardSize  = 20         // keep top N scores
    hmacSecret          = "change-me"// used to sign form state (tamper guard)
)
```

> Rationale: `hmacSecret` allows a simple, stdlib-only integrity check on posted state (prevents users from editing hidden inputs).

---

## Data Files

### `questions.json` (root)
Schema:
```json
[
  {
    "id": "q1",
    "question": "Which zodiac sign is symbolized by the scales?",
    "choices": ["Libra", "Gemini", "Virgo", "Aquarius"],
    "answer_index": 0,
    "explanation": "Libra is represented by the scales, signifying balance."
  }
]
```

### `leaderboard.json`
Array of entries:
```json
[
  { "name": "DAN", "score": 3, "total": 3, "when": "2025-10-21T20:15:00Z" }
]
```

---

## HTTP Endpoints (additive)
- `GET  /` → home with links to quiz & leaderboard  
- `GET  /quiz` → start quiz (select random questions, render first)  
- `POST /quiz` → grade answer, show next question  
- `GET  /quiz/results` → show final score, prompt name entry  
- `POST /quiz/leaderboard` → save score, redirect to leaderboard  
- `GET  /leaderboard` → show top scores  
- `GET  /health` → unchanged

---

## UX Flow
- quiz.html: show question, choices, score, and countdown timer (20s).  
- results.html: show score + percent, name entry, buttons “Save & View Leaderboard” / “Play Again.”  
- leaderboard.html: show table of top N, link to play again.  

---

## Testing
Unit tests for:
- JSON load/validate
- random selection
- HMAC verification
- leaderboard add/sort/truncate
- handler correctness with `httptest`

---

## Developer To-Dos
- Author `questions.json` (10 astrology trivia, 3–4 choices each)
- Implement handlers & templates
- Add tests in `quiz_test.go`
- Pick non-default `hmacSecret`
- Optional: `/api/leaderboard` JSON export

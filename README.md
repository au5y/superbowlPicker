# Superbowl LX Prop Pool


## New Features
- **Auto-Refresh**: The dashboard automatically updates when the game locks or questions are resolved.
- **Theme Toggle**: Switch between Light and Dark mode using the icon in the top right.
- **High Contrast**: Usernames are automatically adjusted for readability in both modes.


## Running the Application

Prerequisites:
- Go
- SQLite (usually included with Go drivers)

To run the application locally:

```bash
go run .
```

The application will start on port 4884 (default) using a local `game.db` SQLite database.

Access the application at: http://localhost:4884

## Admin Interface

To manage the game state and resolve questions, navigate to:

http://localhost:4884/admin?key=touchdown

From here you can:
- Lock/Unlock the game (Open/Locked status).
- Resolve questions (mark correct answers).
- Refresh questions from `questions.json`.
- Manage users (reset PINs, delete users, update rooms).

## Configuration

- `PORT`: Environment variable to set the port (default: 4884).
- `DB_NAME`: Environment variable to set the database file path (default: ./game.db).

This enables you to be able to:
```bash
# Linux/Mac
PORT=4885 DB_NAME=./dev.db go run .
```
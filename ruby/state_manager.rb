class StateManager
  # Configuration imported from config.rb
  
  def initialize
    @current_state = 'UNKNOWN'
    @tension = 0.0 # 0.0 to 10.0 (Atmosphere Heat)
    @last_update = Time.now
  end

  def process_text(text)
    # Check for Danger Words
    hit = Config::DANGER_WORDS.any? { |w| text.include?(w) }
    
    if hit
      @tension += 3.0 # Spike tension
    else
      @tension += 0.2 # Slight activity boost
    end
    
    recalculate
  end
  
  def reset
    @tension = 0.0
    @current_state = 'UNKNOWN'
    recalculate
  end

  def get_state
    # Recalculate periodically even if no new votes, to handle decay
    recalculate 
    {
      state: @current_state,
      tension: @tension,
      split_degree: (@tension / 10.0).clamp(0.0, 1.0), # Map tension to split visual
    }
  end

  private

  def recalculate
    now = Time.now
    dt = now - @last_update
    @last_update = now

    # Natural Decay (Cooling down)
    @tension -= dt * 0.5
    @tension = 0.0 if @tension < 0

    # Tension Thresholds
    if @tension > 8.0
      @current_state = 'SPLIT' # Chaos/Conflict
    elsif @tension > 2.0
      @current_state = 'ALIGNED' # Active Conversation
    else
      @current_state = 'UNKNOWN' # Silence
    end
  end
end
